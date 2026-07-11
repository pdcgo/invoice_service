package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

// CheckOweLimit implements [invoice_ifaceconnect.InvoiceServiceHandler]. It reports,
// per creditor in cfg_team_ids, whether the debtor team (team_id) may still owe them —
// i.e. its current debt is below the creditor's configured threshold.
func (s *invoiceServiceImpl) CheckOweLimit(
	ctx context.Context,
	req *connect.Request[invoice_iface.CheckOweLimitRequest],
) (*connect.Response[invoice_iface.CheckOweLimitResponse], error) {
	pay := req.Msg
	canOwe, err := EvaluateOweLimits(s.db.WithContext(ctx), pay.TeamId, pay.CfgTeamIds)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&invoice_iface.CheckOweLimitResponse{CanOwe: canOwe}), nil
}

// EvaluateOweLimits is the reusable core of the CheckOweLimit RPC: a read-only owe-limit
// evaluation for a debtor against a set of creditors, composable in-process (e.g. the v3
// OrderCreate gate). For each creditor it resolves the owe threshold
// (owe_limit_configurations: custom for this debtor beats the creditor's default; no config
// = allow; threshold 0 = unlimited) and compares it to the debtor's CURRENT debt to that
// creditor read from the invoice v2 ledger (team_balances PAYABLE, stored negative). It is
// purely advisory — the ledger write path enforces nothing; only the caller gates on the
// returned map.
func EvaluateOweLimits(
	db *gorm.DB,
	debtorTeamID uint64,
	creditorTeamIDs []uint64,
) (map[uint64]*invoice_iface.OweLimitAllow, error) {
	result := make(map[uint64]*invoice_iface.OweLimitAllow, len(creditorTeamIDs))
	for _, c := range creditorTeamIDs {
		result[c] = &invoice_iface.OweLimitAllow{Allow: true} // no config => allow (default)
	}
	if len(creditorTeamIDs) == 0 {
		return result, nil
	}

	// 1. Config per creditor: the debtor-specific custom row beats the creditor's default.
	var cfgs []db_models.OweLimitConfiguration
	err := db.
		Model(&db_models.OweLimitConfiguration{}).
		Where("team_id IN ?", creditorTeamIDs).
		Where("for_team_id = ? OR is_default = ?", debtorTeamID, true).
		Find(&cfgs).
		Error
	if err != nil {
		return nil, err
	}

	type limit struct {
		threshold float64
		hasCustom bool
		hasCfg    bool
	}
	limits := map[uint64]*limit{}
	for i := range cfgs {
		cfg := cfgs[i]
		l := limits[cfg.TeamID]
		if l == nil {
			l = &limit{}
			limits[cfg.TeamID] = l
		}
		isCustom := !cfg.IsDefault && cfg.ForTeamID != nil && *cfg.ForTeamID == debtorTeamID
		if isCustom {
			l.threshold = cfg.Threshold
			l.hasCustom = true
			l.hasCfg = true
		} else if cfg.IsDefault && !l.hasCustom {
			l.threshold = cfg.Threshold
			l.hasCfg = true
		}
	}

	// 2. Current debt per creditor: -PAYABLE.balance (PAYABLE is stored negative; absent = 0).
	var balances []invoice_models.TeamBalance
	err = db.
		Model(&invoice_models.TeamBalance{}).
		Where("team_id = ?", debtorTeamID).
		Where("for_team_id IN ?", creditorTeamIDs).
		Where("balance_type = ?", invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE).
		Find(&balances).
		Error
	if err != nil {
		return nil, err
	}
	debtOf := map[uint64]float64{}
	for _, b := range balances {
		debtOf[b.ForTeamID] = -b.Balance
	}

	// 3. Evaluate.
	for _, c := range creditorTeamIDs {
		allow := result[c]
		l := limits[c]
		if l == nil || !l.hasCfg {
			continue // no config => allow (already set)
		}
		debt := debtOf[c]
		allow.Threshold = l.threshold
		allow.ActiveAmount = debt
		if l.threshold == 0 {
			continue // threshold 0 => unlimited (allow already set)
		}
		allow.Allow = debt < l.threshold // current debt below threshold
	}

	return result, nil
}
