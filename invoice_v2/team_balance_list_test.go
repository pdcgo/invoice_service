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
