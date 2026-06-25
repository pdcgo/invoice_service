package invoice_v2

import (
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/invoice_service/invoice_models"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// toProtoBalanceChangeLog maps a stored BalanceChangeLog to its proto representation.
func toProtoBalanceChangeLog(l *invoice_models.BalanceChangeLog) *invoice_iface.BalanceChangeLog {
	return &invoice_iface.BalanceChangeLog{
		Id:           l.ID,
		TeamId:       l.TeamID,
		ForTeamId:    l.ForTeamID,
		ChangeType:   l.ChangeType,
		ChangeAmount: l.ChangeAmount,
		BalanceType:  l.BalanceType,
		Balance:      l.Balance,
		Note:         l.Note,
		CreatedAt:    timestamppb.New(l.CreatedAt),
	}
}
