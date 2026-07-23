package invoice_v2

import (
	"context"

	"connectrpc.com/connect"
	"github.com/pdcgo/invoice_service/invoice_models"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"gorm.io/gorm"
)

func (s *invoiceServiceImpl) GetBalanceChangeSourceByChangeIds(
	ctx context.Context,
	req *connect.Request[invoice_iface.GetBalanceChangeSourceByChangeIdsRequest],
) (*connect.Response[invoice_iface.GetBalanceChangeSourceByChangeIdsResponse], error) {
	pay := req.Msg
	db := s.db.WithContext(ctx)

	entries := map[uint64]*invoice_iface.BalanceChangeSourceEntry{}

	var ownedIDs []uint64
	err := db.
		Model(&invoice_models.BalanceChangeLog{}).
		Where("id IN ? AND team_id = ?", pay.GetBalanceChangeLogIds(), pay.GetTeamId()).
		Pluck("id", &ownedIDs).
		Error
	if err != nil {
		return nil, err
	}
	if len(ownedIDs) == 0 {
		return connect.NewResponse(&invoice_iface.GetBalanceChangeSourceByChangeIdsResponse{Entries: entries}), nil
	}

	entryFor := func(id uint64) *invoice_iface.BalanceChangeSourceEntry {
		e := entries[id]
		if e == nil {
			e = &invoice_iface.BalanceChangeSourceEntry{}
			entries[id] = e
		}
		return e
	}

	byOwned := func(q *gorm.DB) *gorm.DB {
		return q.Where("balance_change_log_id IN ?", ownedIDs)
	}

	var orderRows []invoice_models.BalanceChangeOrderSource
	err = byOwned(db.Model(&invoice_models.BalanceChangeOrderSource{})).Find(&orderRows).Error
	if err != nil {
		return nil, err
	}
	for i := range orderRows {
		row := &orderRows[i]
		entryFor(row.BalanceChangeLogID).Source = &invoice_iface.BalanceChangeSourceEntry_Order{
			Order: toOrderSourceItem(row),
		}
	}

	var restockRows []invoice_models.BalanceChangeRestockSource
	err = byOwned(db.Model(&invoice_models.BalanceChangeRestockSource{})).Find(&restockRows).Error
	if err != nil {
		return nil, err
	}
	for i := range restockRows {
		row := &restockRows[i]
		entryFor(row.BalanceChangeLogID).Source = &invoice_iface.BalanceChangeSourceEntry_Restock{
			Restock: toRestockSourceItem(row),
		}
	}

	var brokenRows []invoice_models.BalanceChangeBrokenSource
	err = byOwned(db.Model(&invoice_models.BalanceChangeBrokenSource{})).Find(&brokenRows).Error
	if err != nil {
		return nil, err
	}
	for i := range brokenRows {
		row := &brokenRows[i]
		entryFor(row.BalanceChangeLogID).Source = &invoice_iface.BalanceChangeSourceEntry_Broken{
			Broken: toBrokenSourceItem(row),
		}
	}

	return connect.NewResponse(&invoice_iface.GetBalanceChangeSourceByChangeIdsResponse{Entries: entries}), nil
}
