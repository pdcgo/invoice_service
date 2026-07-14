package invoice_v2_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/invoice_service/invoice_v2"
	common "github.com/pdcgo/schema/services/common/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// teamRow is a minimal stand-in for the teams table (name + type), avoiding the
// db_models.Team association cascade in AutoMigrate.
type teamRow struct {
	ID   uint64 `gorm:"primarykey"`
	Name string
	Type db_models.TeamType
}

func (teamRow) TableName() string { return "teams" }

func TestTeamBalanceList(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "team balance list",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(tx *gorm.DB) {
				assert.NoError(t, tx.AutoMigrate(
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.InvoicePayment{},
					&db_models.OweLimitConfiguration{},
					&teamRow{},
				))

				inWindow := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				before := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
				after := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
				ptr := func(t time.Time) *time.Time { return &t }

				// counterparties of team 1
				assert.NoError(t, tx.Create(&[]teamRow{
					{ID: 2, Name: "Beta", Type: db_models.SellingTeamType},
					{ID: 3, Name: "Acme", Type: db_models.WarehouseTeamType},
					{ID: 4, Name: "Delta", Type: db_models.AdminTeamType},
				}).Error)

				assert.NoError(t, tx.Create(&[]invoice_models.TeamBalance{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, Balance: -100, PendingPaymentAmount: 30},
					{TeamID: 1, ForTeamID: 3, BalanceType: payable, Balance: -40, PendingPaymentAmount: 0},
					{TeamID: 1, ForTeamID: 2, BalanceType: receivable, Balance: 70, PendingPaymentAmount: 15},
					{TeamID: 1, ForTeamID: 4, BalanceType: receivable, Balance: 25, PendingPaymentAmount: 50},
				}).Error)

				adj := invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT
				whFee := invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE
				prodFee := invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_PRODUCT_FEE
				assert.NoError(t, tx.Create(&[]invoice_models.BalanceChangeLog{
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, ChangeType: adj, ChangeAmount: -60, CreatedAt: inWindow, CreatedByID: 7},
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, ChangeType: whFee, ChangeAmount: -40, CreatedAt: inWindow, CreatedByID: 7},
					{TeamID: 1, ForTeamID: 3, BalanceType: payable, ChangeType: prodFee, ChangeAmount: -40, CreatedAt: inWindow, CreatedByID: 7},
					{TeamID: 1, ForTeamID: 2, BalanceType: payable, ChangeType: adj, ChangeAmount: -500, CreatedAt: before, CreatedByID: 7}, // out of window
				}).Error)

				// only team 2 is paid in-window; team 3 has none (membership test).
				assert.NoError(t, tx.Create(&[]invoice_models.InvoicePayment{
					{TeamID: 1, ForTeamID: 2, Amount: 25, Status: accepted, AcceptedAt: ptr(inWindow), CreatedByID: 7, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 2, Amount: 9, Status: pending, CreatedByID: 7, CreatedAt: inWindow},
					{TeamID: 1, ForTeamID: 2, Amount: 200, Status: accepted, AcceptedAt: ptr(after), CreatedByID: 7, CreatedAt: after}, // out of window
				}).Error)

				// Owe limits, both directions. Team 1 is the scoped team throughout.
				//
				// As CREDITOR, team 1 grants: Beta(2) a custom 150, Acme(3) an explicit
				// 0 (= unlimited), and Delta(4) nothing of its own, so Delta falls back
				// to team 1's default rule of 500.
				//
				// As DEBTOR, the counterparties impose on team 1: Beta(2) a custom 80,
				// Acme(3) its default rule of 60, and Delta(4) nothing at all — so team
				// 1 is unconfigured against Delta.
				teamID := func(id uint64) *uint64 { return &id }
				assert.NoError(t, tx.Create(&[]db_models.OweLimitConfiguration{
					{TeamID: 1, IsDefault: true, Threshold: 500},
					{TeamID: 1, ForTeamID: teamID(2), Threshold: 150},
					{TeamID: 1, ForTeamID: teamID(3), Threshold: 0},
					{TeamID: 2, ForTeamID: teamID(1), Threshold: 80},
					{TeamID: 3, IsDefault: true, Threshold: 60},
				}).Error)

				// Debt owed TO team 1 by each counterparty (the creditor direction reads
				// the counterparty's own PAYABLE row pointing back at team 1). These are
				// scoped to team_id 2/3/4, so they are invisible to every other subtest,
				// which all filter on team_id = 1.
				//
				// The last row is a pair nobody has configured — Delta(4) owes Beta(2)
				// 75, and Beta has neither a custom row for Delta nor a default rule.
				// It exists to prove an unconfigured pair still reports its real debt.
				assert.NoError(t, tx.Create(&[]invoice_models.TeamBalance{
					{TeamID: 2, ForTeamID: 1, BalanceType: payable, Balance: -200},
					{TeamID: 3, ForTeamID: 1, BalanceType: payable, Balance: -10},
					{TeamID: 4, ForTeamID: 1, BalanceType: payable, Balance: -30},
					{TeamID: 4, ForTeamID: 2, BalanceType: payable, Balance: -75},
				}).Error)

				svc := invoice_v2.NewInvoiceService(tx)
				ctx := context.Background()
				window := &invoice_iface.TeamBalanceListTimeFilter{
					Start: timestamppb.New(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
					End:   timestamppb.New(time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC)),
				}
				sortBy := func(s invoice_iface.SortType, oneof *invoice_iface.TeamBalanceListSort) *invoice_iface.TeamBalanceListSort {
					oneof.SortType = s
					return oneof
				}

				t.Run("sort payable desc with common + payable data", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1},
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_DESC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_Payable{Payable: invoice_iface.TeamBalancePayableSort_TEAM_BALANCE_PAYABLE_SORT_BALANCE},
						}),
						DataTypes: []invoice_iface.TeamBalanceListDataType{
							invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON,
							invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_PAYABLE,
						},
					}))
					assert.NoError(t, err)
					// payable balance desc: -40 (team 3) before -100 (team 2)
					assert.Equal(t, []uint64{3, 2}, res.Msg.Ids)
					assert.Len(t, res.Msg.Data, 2)

					cmn := res.Msg.Data[0].GetCommon().GetData()
					assert.Equal(t, "Acme", cmn[3].Name)
					assert.Equal(t, common.TeamType_TEAM_TYPE_WAREHOUSE.String(), cmn[3].Type.String())
					assert.Equal(t, "Beta", cmn[2].Name)

					pay := res.Msg.Data[1].GetPayable().GetData()
					assert.Equal(t, float64(-100), pay[2].Balance)
					assert.Equal(t, float64(-40), pay[3].Balance)
				})

				t.Run("sort by total_payment defines membership (excludes the unpaid counterparty)", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1},
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_DESC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_TotalPayment{TotalPayment: invoice_iface.TeamBalanceTotalPaymentSort_TEAM_BALANCE_TOTAL_PAYMENT_SORT_AMOUNT},
						}),
						DataTypes: []invoice_iface.TeamBalanceListDataType{
							invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_TOTAL_PAYMENT,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2}, res.Msg.Ids) // team 3 has no payment in window
					tp := res.Msg.Data[0].GetTotalPayment().GetData()
					assert.Equal(t, float64(25), tp[2].Amount) // pending + out-of-window excluded
				})

				t.Run("total_payable carries the windowed total + change breakdown", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1},
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_ASC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_Payable{Payable: invoice_iface.TeamBalancePayableSort_TEAM_BALANCE_PAYABLE_SORT_BALANCE},
						}),
						DataTypes: []invoice_iface.TeamBalanceListDataType{
							invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_TOTAL_PAYABLE,
						},
					}))
					assert.NoError(t, err)
					tp := res.Msg.Data[0].GetTotalPayable().GetData()
					assert.Equal(t, float64(-100), tp[2].TotalAmount) // -60 + -40, the -500 is out of window
					assert.Len(t, tp[2].Change, 2)
					byType := map[invoice_iface.BalanceChangeType]float64{}
					for _, c := range tp[2].Change {
						byType[c.ChangeType] = c.Amount
					}
					assert.Equal(t, float64(-60), byType[adj])
					assert.Equal(t, float64(-40), byType[whFee])
					assert.Equal(t, float64(-40), tp[3].TotalAmount)
				})

				t.Run("pagination pages the sorted ids", func(t *testing.T) {
					req := func(page int64) *connect.Request[invoice_iface.TeamBalanceListRequest] {
						return connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
							TimeRange: window,
							Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1, Page: &common.PageFilter{Page: page, Limit: 1}},
							Sort: sortBy(invoice_iface.SortType_SORT_TYPE_DESC, &invoice_iface.TeamBalanceListSort{
								S: &invoice_iface.TeamBalanceListSort_Payable{Payable: invoice_iface.TeamBalancePayableSort_TEAM_BALANCE_PAYABLE_SORT_BALANCE},
							}),
							DataTypes: []invoice_iface.TeamBalanceListDataType{invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_PAYABLE},
						})
					}
					p1, err := svc.TeamBalanceList(ctx, req(1))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{3}, p1.Msg.Ids)
					p2, err := svc.TeamBalanceList(ctx, req(2))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2}, p2.Msg.Ids)
				})

				t.Run("sort incoming_payment desc with incoming_payment data (receivable-side pending)", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1},
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_DESC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_IncomingPayment{
								IncomingPayment: invoice_iface.TeamBalanceIncomingPaymentSort_TEAM_BALANCE_INCOMING_PAYMENT_SORT_AMOUNT,
							},
						}),
						DataTypes: []invoice_iface.TeamBalanceListDataType{
							invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_INCOMING_PAYMENT,
						},
					}))
					assert.NoError(t, err)
					// receivable pending desc: 50 (team 4) before 15 (team 2)
					assert.Equal(t, []uint64{4, 2}, res.Msg.Ids)
					inc := res.Msg.Data[0].GetIncomingPayment().GetData()
					assert.Equal(t, float64(50), inc[4].Amount)
					assert.Equal(t, float64(15), inc[2].Amount)
				})

				t.Run("team_type filter narrows the counterparties", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1, TeamType: common.TeamType_TEAM_TYPE_SELLING},
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_ASC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_Common{Common: invoice_iface.TeamBalanceCommonSort_TEAM_BALANCE_COMMON_SORT_TEAM_NAME},
						}),
						DataTypes: []invoice_iface.TeamBalanceListDataType{invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2}, res.Msg.Ids) // only Beta is a selling team
				})

				// listReq is the common shape for the search / owe-limit subtests: sorted
				// by team name ascending, which puts every counterparty in the page.
				listReq := func(f *invoice_iface.TeamBalanceListFilter, dts ...invoice_iface.TeamBalanceListDataType) *connect.Request[invoice_iface.TeamBalanceListRequest] {
					return connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    f,
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_ASC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_Common{Common: invoice_iface.TeamBalanceCommonSort_TEAM_BALANCE_COMMON_SORT_TEAM_NAME},
						}),
						DataTypes: dts,
					})
				}

				t.Run("q filters counterparties by name, case-insensitively", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, listReq(
						&invoice_iface.TeamBalanceListFilter{TeamId: 1, Q: "ac"},
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON,
					))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{3}, res.Msg.Ids) // "Acme" only, matched lowercase
					assert.Equal(t, "Acme", res.Msg.Data[0].GetCommon().GetData()[3].Name)
				})

				t.Run("q also filters on a sort that does not otherwise join teams", func(t *testing.T) {
					// Regression: the payable sort reads only team_balances. If the name
					// search does not force the teams join, it silently matches everything
					// and this returns {3, 2} instead of {2}.
					res, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1, Q: "beta"},
						Sort: sortBy(invoice_iface.SortType_SORT_TYPE_DESC, &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_Payable{Payable: invoice_iface.TeamBalancePayableSort_TEAM_BALANCE_PAYABLE_SORT_BALANCE},
						}),
						DataTypes: []invoice_iface.TeamBalanceListDataType{invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_PAYABLE},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2}, res.Msg.Ids)
					assert.Equal(t, float64(-100), res.Msg.Data[0].GetPayable().GetData()[2].Balance)
				})

				t.Run("q ANDs with the team_type filter without joining teams twice", func(t *testing.T) {
					// "a" matches Beta, Acme and Delta; only Beta is a selling team.
					res, err := svc.TeamBalanceList(ctx, listReq(
						&invoice_iface.TeamBalanceListFilter{TeamId: 1, Q: "a", TeamType: common.TeamType_TEAM_TYPE_SELLING},
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON,
					))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2}, res.Msg.Ids)
				})

				t.Run("q matching nothing returns no ids and empty data", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, listReq(
						&invoice_iface.TeamBalanceListFilter{TeamId: 1, Q: "zzz"},
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON,
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_OWE_LIMIT_AS_CREDITOR,
					))
					assert.NoError(t, err)
					assert.Empty(t, res.Msg.Ids)
					assert.Empty(t, res.Msg.Data[0].GetCommon().GetData())
					assert.Empty(t, res.Msg.Data[1].GetOweLimitAsCreditor().GetData())
				})

				t.Run("owe_limit as_creditor: custom beats default, 0 is unlimited", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, listReq(
						&invoice_iface.TeamBalanceListFilter{TeamId: 1},
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_COMMON,
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_OWE_LIMIT_AS_CREDITOR,
					))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{3, 2, 4}, res.Msg.Ids) // Acme, Beta, Delta by name

					ol := res.Msg.Data[1].GetOweLimitAsCreditor().GetData()

					// Beta: custom 150 beats team 1's default 500; it owes 200, so it is
					// over the limit. The boundary is strict — debt >= threshold blocks.
					assert.Equal(t, float64(150), ol[2].Threshold)
					assert.Equal(t, float64(200), ol[2].ActiveAmount)
					assert.True(t, ol[2].Configured)
					assert.False(t, ol[2].IsDefault)
					assert.False(t, ol[2].Allow)

					// Acme: custom threshold 0 = unlimited, so allowed despite the debt.
					assert.Equal(t, float64(0), ol[3].Threshold)
					assert.Equal(t, float64(10), ol[3].ActiveAmount)
					assert.True(t, ol[3].Configured)
					assert.True(t, ol[3].Allow)

					// Delta: no custom row, falls back to team 1's default rule of 500.
					assert.Equal(t, float64(500), ol[4].Threshold)
					assert.Equal(t, float64(30), ol[4].ActiveAmount)
					assert.True(t, ol[4].Configured)
					assert.True(t, ol[4].IsDefault)
					assert.True(t, ol[4].Allow)
				})

				t.Run("owe_limit as_debtor: the limits counterparties impose on us", func(t *testing.T) {
					res, err := svc.TeamBalanceList(ctx, listReq(
						&invoice_iface.TeamBalanceListFilter{TeamId: 1},
						invoice_iface.TeamBalanceListDataType_TEAM_BALANCE_LIST_DATA_TYPE_OWE_LIMIT_AS_DEBTOR,
					))
					assert.NoError(t, err)
					ol := res.Msg.Data[0].GetOweLimitAsDebtor().GetData()

					// Beta caps us at 80 and we owe it 100 — blocked.
					assert.Equal(t, float64(80), ol[2].Threshold)
					assert.Equal(t, float64(100), ol[2].ActiveAmount)
					assert.True(t, ol[2].Configured)
					assert.False(t, ol[2].Allow)

					// Acme has no row for us, so its default rule of 60 applies; we owe 40.
					assert.Equal(t, float64(60), ol[3].Threshold)
					assert.Equal(t, float64(40), ol[3].ActiveAmount)
					assert.True(t, ol[3].Configured)
					assert.True(t, ol[3].IsDefault)
					assert.True(t, ol[3].Allow)

					// Delta imposes nothing at all: unconfigured, and allowed.
					assert.False(t, ol[4].Configured)
					assert.True(t, ol[4].Allow)
				})

				t.Run("an unconfigured pair still reports its real debt", func(t *testing.T) {
					// The gate's EvaluateOweLimits leaves ActiveAmount at 0 when no config
					// applies, which a list column cannot do — the reader could not tell it
					// apart from a team that owes nothing. Delta owes Beta 75 and Beta has
					// no rule for it, so it must come back unconfigured, allowed, and 75.
					evals, err := invoice_v2.EvaluateOweLimitsAsDebtor(tx, 4, []uint64{2})
					assert.NoError(t, err)
					assert.False(t, evals[2].Configured)
					assert.True(t, evals[2].Allow)
					assert.Equal(t, float64(75), evals[2].ActiveAmount)
					assert.Equal(t, float64(0), evals[2].Threshold)
				})

				t.Run("missing time_range / sort is invalid", func(t *testing.T) {
					_, err := svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						Filter: &invoice_iface.TeamBalanceListFilter{TeamId: 1},
						Sort: &invoice_iface.TeamBalanceListSort{
							S: &invoice_iface.TeamBalanceListSort_Payable{Payable: invoice_iface.TeamBalancePayableSort_TEAM_BALANCE_PAYABLE_SORT_BALANCE},
						},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					_, err = svc.TeamBalanceList(ctx, connect.NewRequest(&invoice_iface.TeamBalanceListRequest{
						TimeRange: window,
						Filter:    &invoice_iface.TeamBalanceListFilter{TeamId: 1},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		})
}
