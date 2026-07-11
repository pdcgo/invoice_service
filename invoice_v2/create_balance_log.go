package invoice_v2

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/user_service/access_interceptors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateBalanceLog implements [invoice_ifaceconnect.InvoiceServiceHandler].
//
// It posts a double-entry balance change for the (team, for_team) pair: the
// primary leg moves balance_type by +amount, and the mirrored counter leg moves
// the opposite balance_type (with swapped teams) by -amount. Both legs update
// the running TeamBalance, append an immutable BalanceChangeLog, and accumulate
// a per-day TeamBalanceDailyLog. The whole thing runs in one transaction.
func (s *invoiceServiceImpl) CreateBalanceLog(
	ctx context.Context,
	req *connect.Request[invoice_iface.CreateBalanceLogRequest],
) (*connect.Response[invoice_iface.CreateBalanceLogResponse], error) {
	pay := req.Msg

	// The caller (set by the access interceptor) owns both ledger legs.
	caller, err := access_interceptors.GetIdentityFromCtx(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	createdByID := uint64(caller.IdentityId)

	now := time.Now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return PostBalanceLog(tx, pay.TeamId, pay.ForTeamId, pay.ChangeType, pay.ChangeAmount, pay.BalanceType, pay.Note, createdByID, now)
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&invoice_iface.CreateBalanceLogResponse{}), nil
}

// OrderSource attributes the posted ledger legs to the order that caused them
// (order-driven fees). When passed, both the primary and mirror legs get a
// BalanceChangeOrderSource row keyed by their balance_change_log id. OrderSystem
// disambiguates the legacy vs v3 order id-space. TeamID is the canonical ordering
// team (constant across legs and create/cancel), not the leg's own team.
type OrderSource struct {
	OrderSystem invoice_iface.OrderSystem
	OrderID     uint64
	TeamID      uint64
	WarehouseID uint64
}

// PostBalanceLog validates and posts a double-entry balance change within the
// caller's transaction. It is the reusable core of the CreateBalanceLog RPC, so
// it can be composed into any db.Transaction scope (e.g. event/push handlers).
// It takes createdByID/now as params (no ctx identity lookup) so non-RPC callers
// can supply a system id and their own clock. An optional OrderSource attaches
// order attribution to both ledger legs.
func PostBalanceLog(
	tx *gorm.DB,
	teamID, forTeamID uint64,
	changeType invoice_iface.BalanceChangeType,
	changeAmount float64,
	balanceType invoice_iface.BalanceType,
	note string,
	createdByID uint64,
	now time.Time,
	src ...*OrderSource,
) error {
	if teamID == 0 || forTeamID == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("team_id and for_team_id are required"))
	}
	if teamID == forTeamID {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("team_id and for_team_id must differ"))
	}
	if changeAmount <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("change_amount must be greater than zero"))
	}
	if changeType == invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_UNSPECIFIED {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("change_type is required"))
	}
	if _, err := oppositeBalance(balanceType); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	var orderSource *OrderSource
	if len(src) > 0 {
		orderSource = src[0]
	}
	return postDoubleEntry(tx, teamID, forTeamID, balanceType, changeType, changeAmount, note, createdByID, now, orderSource)
}

// postDoubleEntry posts a signed-mirror double entry for the (teamID, forTeamID)
// pair: +amount on balance type bt, and -amount on the opposite type with the
// teams swapped.
func postDoubleEntry(
	tx *gorm.DB,
	teamID, forTeamID uint64,
	bt invoice_iface.BalanceType,
	changeType invoice_iface.BalanceChangeType,
	amount float64,
	note string,
	createdByID uint64,
	now time.Time,
	src *OrderSource,
) error {
	counterType, err := oppositeBalance(bt)
	if err != nil {
		return err
	}
	if err := postEntry(tx, teamID, forTeamID, bt, changeType, amount, note, createdByID, now, src); err != nil {
		return err
	}
	return postEntry(tx, forTeamID, teamID, counterType, changeType, -amount, note, createdByID, now, src)
}

// postEntry applies a single signed delta to one (team, for_team, balance_type)
// account: it locks/loads (or creates) the TeamBalance, writes a BalanceChangeLog
// with the resulting balance, and accumulates the day's TeamBalanceDailyLog.
func postEntry(
	tx *gorm.DB,
	teamID, forTeamID uint64,
	bt invoice_iface.BalanceType,
	changeType invoice_iface.BalanceChangeType,
	delta float64,
	note string,
	createdByID uint64,
	now time.Time,
	src *OrderSource,
) error {
	bal, err := lockOrCreateBalance(tx, teamID, forTeamID, bt, now)
	if err != nil {
		return err
	}

	prev := bal.Balance
	newBal := prev + delta

	if err := tx.Model(&invoice_models.TeamBalance{}).
		Where("id = ?", bal.ID).
		Updates(map[string]interface{}{
			"balance":    newBal,
			"updated_at": now,
		}).Error; err != nil {
		return err
	}

	logEntry := invoice_models.BalanceChangeLog{
		TeamID:       teamID,
		ForTeamID:    forTeamID,
		ChangeType:   changeType,
		ChangeAmount: delta,
		BalanceType:  bt,
		Balance:      newBal,
		Note:         note,
		CreatedByID:  createdByID,
		CreatedAt:    now,
	}
	if err := tx.Create(&logEntry).Error; err != nil {
		return err
	}

	// Attach order attribution to this leg (both legs of a double entry carry the
	// same OrderSource, so the order's full create+reverse fee history is queryable).
	if src != nil {
		source := invoice_models.BalanceChangeOrderSource{
			BalanceChangeLogID: logEntry.ID,
			OrderSystem:        src.OrderSystem,
			OrderID:            src.OrderID,
			TeamID:             src.TeamID,
			WarehouseID:        src.WarehouseID,
			CreatedAt:          now,
		}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
	}

	return upsertDailyLog(tx, teamID, forTeamID, bt, prev, newBal, delta, now)
}

// upsertDailyLog accumulates the day's change for the account: on the first
// change of the day it records StartBalance (the balance before this change);
// subsequent changes add to ChangeAmount and move EndBalance.
func upsertDailyLog(
	tx *gorm.DB,
	teamID, forTeamID uint64,
	bt invoice_iface.BalanceType,
	prev, newBal, delta float64,
	now time.Time,
) error {
	day := startOfJakartaDay(now)

	// TeamBalanceDailyLog has no primary key in the model, so use Find +
	// RowsAffected (First would add ORDER BY <pk> and fail on a key-less model).
	var daily invoice_models.TeamBalanceDailyLog
	res := lockForUpdate(tx).
		Where("day = ? AND team_id = ? AND for_team_id = ? AND balance_type = ?", day, teamID, forTeamID, bt).
		Limit(1).
		Find(&daily)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return tx.Model(&invoice_models.TeamBalanceDailyLog{}).
			Where("day = ? AND team_id = ? AND for_team_id = ? AND balance_type = ?", day, teamID, forTeamID, bt).
			Updates(map[string]interface{}{
				"change_amount": daily.ChangeAmount + delta,
				"end_balance":   newBal,
				"updated_at":    now,
			}).Error
	}

	daily = invoice_models.TeamBalanceDailyLog{
		Day:          day,
		TeamID:       teamID,
		ForTeamID:    forTeamID,
		BalanceType:  bt,
		StartBalance: prev,
		EndBalance:   newBal,
		ChangeAmount: delta,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return tx.Create(&daily).Error
}

// lockOrCreateBalance locks the TeamBalance row for (teamID, forTeamID, bt) for
// update, creating a zero row if none exists.
func lockOrCreateBalance(
	tx *gorm.DB,
	teamID, forTeamID uint64,
	bt invoice_iface.BalanceType,
	now time.Time,
) (*invoice_models.TeamBalance, error) {
	var bal invoice_models.TeamBalance
	err := lockForUpdate(tx).
		Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).
		First(&bal).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		bal = invoice_models.TeamBalance{
			TeamID:      teamID,
			ForTeamID:   forTeamID,
			BalanceType: bt,
			Balance:     0,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&bal).Error; err != nil {
			return nil, err
		}
	}
	return &bal, nil
}

// adjustPending moves the PendingPaymentAmount of one (team, for_team,
// balance_type) account by delta (locking/creating the row as needed).
func adjustPending(
	tx *gorm.DB,
	teamID, forTeamID uint64,
	bt invoice_iface.BalanceType,
	delta float64,
	now time.Time,
) error {
	bal, err := lockOrCreateBalance(tx, teamID, forTeamID, bt, now)
	if err != nil {
		return err
	}
	return tx.Model(&invoice_models.TeamBalance{}).
		Where("id = ?", bal.ID).
		Updates(map[string]interface{}{
			"pending_payment_amount": bal.PendingPaymentAmount + delta,
			"updated_at":             now,
		}).Error
}

// oppositeBalance returns the mirrored balance type for the double entry.
func oppositeBalance(bt invoice_iface.BalanceType) (invoice_iface.BalanceType, error) {
	switch bt {
	case invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE:
		return invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE, nil
	case invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE:
		return invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE, nil
	default:
		return invoice_iface.BalanceType_BALANCE_TYPE_UNSPECIFIED, errors.New("balance_type must be PAYABLE or RECEIVABLE")
	}
}

// jakartaZone is the canonical business timezone (Asia/Jakarta, fixed UTC+7, no DST).
// Day-bucketing (daily log Day, timeline period buckets) is defined in this zone so it
// is stable across deploy environments (the process/DB TZ does not shift the boundary).
var jakartaZone = time.FixedZone("Asia/Jakarta", 7*60*60)

// startOfJakartaDay truncates t to midnight in Asia/Jakarta.
func startOfJakartaDay(t time.Time) time.Time {
	j := t.In(jakartaZone)
	return time.Date(j.Year(), j.Month(), j.Day(), 0, 0, 0, 0, jakartaZone)
}

// lockForUpdate applies SELECT ... FOR UPDATE row locking.
func lockForUpdate(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}
