package invoice_service

import (
	"context"
	"net/http"

	"buf.build/go/protovalidate"
	"github.com/pdcgo/event_source"
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/shared/pkg/common_helper"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

type InvoicePushHttpHandler http.HandlerFunc

func NewInvoicePushHttpHandler(handler InvoicePushHandler) InvoicePushHttpHandler {
	return InvoicePushHttpHandler(event_source.NewMuxPushhandler(event_source.PushHandler(handler)))
}

type InvoicePushHandler event_source.PushHandler

func NewInvoicePushHandler(db *gorm.DB, eventSender event_source.EventSender) InvoicePushHandler {
	return func(ctx context.Context, msg *event_source.PushRequest) error {
		var err error

		var event invoice_iface.InvoiceEvent
		err = protojson.Unmarshal(msg.Message.Data, &event)
		if err != nil {
			return err
		}

		// validating message
		err = protovalidate.GlobalValidator.Validate(&event)
		if err != nil {
			return err
		}

		return db.Transaction(func(tx *gorm.DB) error {
			handler := common_helper.NewChainParam(
				func(next common_helper.NextFuncParam[*invoice_iface.InvoiceEvent]) common_helper.NextFuncParam[*invoice_iface.InvoiceEvent] {
					return func(event *invoice_iface.InvoiceEvent) (*invoice_iface.InvoiceEvent, error) { // writing to log

						// switch eventData := event.Data.(type) {

						// }

						return next(event)
					}

				},

				func(next common_helper.NextFuncParam[*invoice_iface.InvoiceEvent]) common_helper.NextFuncParam[*invoice_iface.InvoiceEvent] {
					return func(event *invoice_iface.InvoiceEvent) (*invoice_iface.InvoiceEvent, error) { // kalkulasi statistik
						return next(event)
					}

				},
			)

			_, err = handler(&event)
			return err
		})
	}
}

type balanceManager struct {
	db *gorm.DB
}

func NewBalanceManager(db *gorm.DB) *balanceManager {
	return &balanceManager{
		db: db,
	}
}

func (b *balanceManager) ProcessInvoiceLog(logs []*invoice_models.InvoiceLog) error {
	var err error
	// err := b.prepareBalance(logs)
	// if err != nil {
	// 	return err
	// }

	// for _, log := range logs {
	// 	var teamId, toTeamId uint64
	// 	var amount float64
	// 	switch log.LogType {
	// 	case invoice_iface.InvoiceLogType_SHIPPING_FEE, invoice_iface.InvoiceLogType_WAREHOUSE_FEE:
	// 		teamId = log.FromTeamID
	// 		toTeamId = log.ToTeamID

	// 	}

	// }

	return err
}

// func (b *balanceManager) prepareBalance(logs []*invoice_models.InvoiceLog) error {
// 	var err error

// 	var teamIds, toTeamIds []uint64
// 	var teamIdMap, toTeamIdMap map[uint64]bool
// 	for _, log := range logs {

// 		if teamIdMap[log.FromTeamID] == false {
// 			teamIdMap[log.FromTeamID] = true
// 			teamIds = append(teamIds, log.FromTeamID)
// 		}

// 		if toTeamIdMap[log.ToTeamID] == false {
// 			toTeamIdMap[log.ToTeamID] = true
// 			forTeamIds = append(forTeamIds, log.ToTeamID)
// 		}

// 	}

// 	// creating balance
// 	for _, teamId := range teamIds {
// 		for _, forTeamId := range forTeamIds {

// 			own := invoice_models.Balance{
// 				TeamID:   teamId,
// 				ToTeamID: forTeamId,
// 				Balance:  0,
// 			}

// 			opposite := invoice_models.Balance{
// 				TeamID:   forTeamId,
// 				ToTeamID: teamId,
// 				Balance:  0,
// 			}

// 			err = b.
// 				db.
// 				Clauses(clause.OnConflict{DoNothing: true}).Create(&[]invoice_models.Balance{own, opposite}).
// 				Error

// 			if err != nil {
// 				return err
// 			}

// 		}
// 	}

// 	return err

// }
