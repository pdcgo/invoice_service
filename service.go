package invoice_service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/interfaces/invoice_iface"
	"github.com/pdcgo/shared/pkg/db_tools"
	"github.com/pdcgo/shared/pkg/ware_cache"
	"gorm.io/gorm"
)

type InvoiceLimitConfiguration struct {
	ID        uint                    `gorm:"primarykey" json:"id"`
	LimitType invoice_iface.LimitType `json:"limit_type"`
	TeamID    int64                   `json:"team_id"`
	ForTeamID *int64                  `json:"for_team_id"`
	Threshold float64                 `json:"threshold"`
}

type invoiceServiceImpl struct {
	invoice_iface.UnimplementedInvoiceServiceServer
	db    *gorm.DB
	cache ware_cache.Cache
}

func NewInvoiceService(db *gorm.DB, cache ware_cache.Cache) invoice_iface.InvoiceServiceServer {
	return &invoiceServiceImpl{
		db:    db,
		cache: cache,
	}
}

func (i *invoiceServiceImpl) cacheKey(pay *invoice_iface.TeamLimitInvoiceReq) string {
	return fmt.Sprintf("invoice_service:limit_invoice-%d-%d", pay.TeamId, pay.ForTeamId)
}

// GetLimitInvoice implements invoice_iface.InvoiceServiceServer.
func (i *invoiceServiceImpl) GetLimitInvoice(ctx context.Context, pay *invoice_iface.TeamLimitInvoiceReq) (*invoice_iface.TeamLimitInvoiceRes, error) {
	if pay.TeamId == 0 {
		return nil, errors.New("team_id kosong")
	}

	// if pay.ForTeamId == 0 {
	// 	return nil, errors.New("for_team_id kosong")
	// }

	res := invoice_iface.TeamLimitInvoiceRes{
		TeamId:              pay.TeamId,
		ForTeamId:           pay.ForTeamId,
		UnpaidInvoiceAmount: 0,
	}
	key := i.cacheKey(pay)
	err := i.cache.Get(ctx, key, &res)
	if err != nil {
		if !errors.Is(err, ware_cache.ErrCacheMiss) {
			return &res, err
		}
	} else {
		return &res, nil
	}

	err = i.db.Raw(`
select 
	(case when sum(amount) is null then 0 else sum(amount) end) as amount 
from invoices i 
where 
	i.status = 'not_paid'
	and i.from_team_id = ?
	and i.to_team_id = ?
	`, pay.ForTeamId, pay.TeamId).
		Find(&res.UnpaidInvoiceAmount).
		Error

	if err != nil {
		return &res, err
	}

	configs := []*InvoiceLimitConfiguration{}

	err = i.
		db.
		Model(&InvoiceLimitConfiguration{}).
		Where("team_id = ? and (limit_type = ? or for_team_id = ?)", pay.TeamId, invoice_iface.LimitType_DEFAULT, pay.ForTeamId).
		Find(&configs).
		Error

	configmap := map[invoice_iface.LimitType]*InvoiceLimitConfiguration{}
	for _, d := range configs {
		conf := d
		configmap[conf.LimitType] = conf
	}

	limitter := configmap[invoice_iface.LimitType_DEFAULT]
	if configmap[invoice_iface.LimitType_TEAM] != nil {
		limitter = configmap[invoice_iface.LimitType_TEAM]
	}

	if limitter == nil {
		limitter = &InvoiceLimitConfiguration{
			LimitType: invoice_iface.LimitType_DEFAULT,
			TeamID:    pay.TeamId,
			Threshold: 0,
		}
	}

	res.LimitThressholdAmount = limitter.Threshold
	res.LimitType = limitter.LimitType
	res.CanCreateOrder = false

	if limitter.Threshold == 0 {
		res.CanCreateOrder = true
	} else if res.UnpaidInvoiceAmount < res.LimitThressholdAmount {
		res.CanCreateOrder = true
	}

	if err != nil {
		return &res, err
	}

	err = i.cache.Add(ctx, &ware_cache.CacheItem{
		Key:        key,
		Expiration: time.Minute,
		Data:       &res,
	})

	return &res, err

}

// LimitInvoiceList implements invoice_iface.InvoiceServiceServer.
func (i *invoiceServiceImpl) LimitInvoiceList(ctx context.Context, pay *invoice_iface.ConfigListReq) (*invoice_iface.ConfigListRes, error) {
	datas := []*invoice_iface.ConfigItem{}
	err := i.
		db.
		Model(&InvoiceLimitConfiguration{}).
		Where("team_id = ?", pay.TeamId).
		Find(&datas).
		Error

	if err != nil {
		return nil, err
	}

	err = db_tools.Preload(
		datas,
		func(i int, item *invoice_iface.ConfigItem) []int64 {
			if item.ForTeamId != nil {
				return []int64{*item.ForTeamId, item.TeamId}
			}
			return []int64{item.TeamId}
		},
		func(ids []int64) (*gorm.DB, func(*invoice_iface.TeamInfo) int64) {
			q := i.db.
				Model(&db_models.Team{}).
				Where("id in ?", ids)

			return q,
				func(p *invoice_iface.TeamInfo) int64 {
					return p.Id
				}
		},
		func(i int, datamap map[int64]*invoice_iface.TeamInfo) {
			datas[i].Team = datamap[datas[i].TeamId]
			if datas[i].ForTeamId != nil {
				datas[i].ForTeam = datamap[*datas[i].ForTeamId]
			}
		},
	)

	res := invoice_iface.ConfigListRes{
		Data: datas,
	}

	return &res, err

}

// LimitInvoiceDelete implements invoice_iface.InvoiceServiceServer.
func (i *invoiceServiceImpl) LimitInvoiceDelete(ctx context.Context, pay *invoice_iface.LimitInvoiceDeleteReq) (*invoice_iface.CommonRes, error) {
	// check data
	if pay.TeamId == 0 {
		return nil, errors.New("team_id tidak ada")
	}

	query := i.
		db.
		Model(&InvoiceLimitConfiguration{}).
		Where("team_id = ?", pay.TeamId)

	if pay.ForTeamId != nil {
		query = query.
			Where("for_team_id = ?", pay.ForTeamId)
	}
	err := query.Delete(&InvoiceLimitConfiguration{}).Error
	res := invoice_iface.CommonRes{}
	return &res, err

}

// SetLimitInvoice implements invoice_iface.InvoiceServiceServer.
func (i *invoiceServiceImpl) SetLimitInvoice(ctx context.Context, pay *invoice_iface.SetLimitInvoiceReq) (*invoice_iface.SetLimitInvoiceRes, error) {
	// check data
	if pay.TeamId == 0 {
		return nil, errors.New("team_id tidak ada")
	}

	te := db_models.Team{}
	err := i.db.Model(&db_models.Team{}).First(&te, pay.TeamId).Error
	if err != nil {
		return nil, err
	}

	forte := db_models.Team{}
	if pay.ForTeamId != nil {
		err = i.db.Model(&db_models.Team{}).First(&forte, *pay.ForTeamId).Error
		if err != nil {
			return nil, err
		}
	}

	conf := InvoiceLimitConfiguration{
		LimitType: invoice_iface.LimitType_DEFAULT,
		TeamID:    pay.TeamId,
	}

	res := invoice_iface.SetLimitInvoiceRes{
		Message: "success",
	}

	if pay.ForTeamId != nil {
		conf.ForTeamID = pay.ForTeamId
		conf.LimitType = invoice_iface.LimitType_TEAM
	}

	confSqlQuery := i.db.Model(&InvoiceLimitConfiguration{}).
		Where("team_id = ?", pay.TeamId).
		Where("limit_type = ?", conf.LimitType)
	if conf.ForTeamID != nil {
		confSqlQuery = confSqlQuery.Where("for_team_id = ?", pay.ForTeamId)
	}

	err = confSqlQuery.
		First(&conf).
		Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	conf.Threshold = pay.Threshold
	err = i.db.Save(&conf).Error
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func (i *invoiceServiceImpl) DeterminedLimitInvoice(ctx context.Context, pay *invoice_iface.DeterminedConfigListReq) (*invoice_iface.DeterminedConfigListRes, error) {
	data := []*invoice_iface.DetermineConfigItem{}

	tempData := []*InvoiceLimitConfiguration{}
	err := i.
		db.
		Model(&InvoiceLimitConfiguration{}).
		Select([]string{
			"id",
			"limit_type",
			"team_id",
			"for_team_id",
			"threshold",
		}).
		Where("for_team_id = ?", pay.TeamId).
		Where("team_id IN (?)", pay.FromTeamIds).
		// Or("(limit_type = ? and team_id <> ?)", invoice_iface.LimitType_DEFAULT, pay.TeamId).
		Find(&tempData).
		Error
	if err != nil {
		return nil, err
	}

	if len(tempData) == 0 {
		return &invoice_iface.DeterminedConfigListRes{
			Data: data,
		}, nil
	}

	for _, value := range tempData {
		data = append(data, &invoice_iface.DetermineConfigItem{
			Id:        int64(value.ID),
			LimitType: value.LimitType,
			TeamId:    value.TeamID,
			ForTeamId: value.ForTeamID,
			Threshold: value.Threshold,
		})
	}

	err = db_tools.Preload(
		data,
		func(i int, item *invoice_iface.DetermineConfigItem) []int64 {
			if item.ForTeamId != nil {
				return []int64{*item.ForTeamId, item.TeamId}
			}
			return []int64{item.TeamId}
		},
		func(ids []int64) (*gorm.DB, func(*invoice_iface.TeamInfo) int64) {
			q := i.db.
				Model(&db_models.Team{}).
				Where("id in ?", ids)

			return q,
				func(p *invoice_iface.TeamInfo) int64 {
					return p.Id
				}
		},
		func(i int, datamap map[int64]*invoice_iface.TeamInfo) {
			data[i].Team = datamap[data[i].TeamId]

			if data[i].ForTeamId != nil {
				data[i].ForTeam = datamap[*data[i].ForTeamId]
			}
		},
	)
	if err != nil {
		return nil, err
	}

	err = db_tools.Preload(
		data,
		func(i int, item *invoice_iface.DetermineConfigItem) []int64 {
			return []int64{item.TeamId}
		},
		func(ids []int64) (*gorm.DB, func(item *invoice_iface.TeamInvoiceStatus) int64) {
			q := i.db.
				Model(&db_models.Invoice{}).
				Select([]string{
					"to_team_id",
					"SUM(CASE WHEN (has_submission IS NULL OR has_submission = false) THEN amount END) AS unpaid_amount",
					"SUM(CASE WHEN has_submission = true THEN amount END) AS submission_amount",
					"SUM(amount) AS total",
				}).
				Where("from_team_id = ?", pay.TeamId).
				Where("to_team_id IN (?)", ids).
				Where("status = ?", db_models.InvoiceNotPaid).
				Group("to_team_id")

			return q, func(p *invoice_iface.TeamInvoiceStatus) int64 {
				return p.ToTeamId
			}
		},
		func(i int, datamap map[int64]*invoice_iface.TeamInvoiceStatus) {
			toTeamID := data[i].TeamId

			invoiceStatus, ok := datamap[toTeamID]
			if !ok {
				data[i].InvoiceStatus = &invoice_iface.TeamInvoiceStatus{
					ToTeamId: toTeamID,
				}
				return
			}
			data[i].InvoiceStatus = invoiceStatus
		},
	)
	if err != nil {
		return nil, err
	}

	res := invoice_iface.DeterminedConfigListRes{
		Data: data,
	}

	return &res, nil
}
