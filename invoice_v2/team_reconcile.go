package invoice_v2

import (
	"context"
	"log/slog"
	"math"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
)

func (s *invoiceServiceImpl) TeamReconcile(
	ctx context.Context,
	req *connect.Request[invoice_iface.TeamReconcileRequest],
) (*connect.Response[invoice_iface.TeamReconcileResponse], error) {
	adjustmentCount, err := s.reconcileTeamPayable(ctx, req.Msg.TeamId)

	slog.Info("reconcile invoice", "count", adjustmentCount)

	return connect.NewResponse(&invoice_iface.TeamReconcileResponse{}), err
}

// reconcileEpsilon is the smallest balance difference worth posting (money is
// float64; sub-cent diffs are noise, not real adjustments).
const reconcileEpsilon = 0.005

type payableReconcile struct {
	DeltaBalance  float64
	SourceBalance float64
	Balance       float64
	TeamID        uint64
	ForTeamID     uint64
}

// reconcilePayableSQL computes, per (from_team -> to_team) pair the team owes, the gap between
// the legacy unpaid-invoice total (the PAYABLE target magnitude) and the current TeamBalance
// PAYABLE. In `invoices`, from_team is the debtor, so its PAYABLE should equal -source;
// delta = source + balance is zero when reconciled. balance_type lives in the JOIN (not WHERE)
// and COALESCE defaults a missing balance row to 0, so a brand-new debt still surfaces.
//
// Two positional ? params, bound in textual order: 1st = teamID (CTE), 2nd = PAYABLE (JOIN).
const reconcilePayableSQL = `
WITH d AS (
	SELECT i.from_team_id AS team_id,
	       i.to_team_id   AS for_team_id,
	       SUM(i.amount)  AS source_balance
	FROM invoices i
	WHERE i.status = 'not_paid'
	  AND i.amount > 0
	  AND i.from_team_id = ?
	GROUP BY i.from_team_id, i.to_team_id
)
SELECT (d.source_balance + COALESCE(tb.balance, 0)) AS delta_balance,
       d.source_balance                              AS source_balance,
       COALESCE(tb.balance, 0)                       AS balance,
       d.team_id                                     AS team_id,
       d.for_team_id                                 AS for_team_id
FROM d
LEFT JOIN team_balances tb
	ON tb.team_id = d.team_id
	AND tb.for_team_id = d.for_team_id
	AND tb.balance_type = ?
WHERE (d.source_balance + COALESCE(tb.balance, 0)) != 0
`

// reconcileTeamPayable reconciles the team's PAYABLE balances to the legacy unpaid-invoice
// totals: for each (from_team -> to_team) pair where the team is the debtor, it posts the delta
// as an ADJUSTMENT so the stored PAYABLE matches -source. Because it posts only the delta,
// re-running converges (delta -> 0) and is idempotent.
func (s *invoiceServiceImpl) reconcileTeamPayable(ctx context.Context, teamID uint64) (int, error) {
	var list []*payableReconcile
	err := s.db.WithContext(ctx).
		Raw(reconcilePayableSQL,
			teamID,
			int32(invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE),
		).
		Scan(&list).Error
	if err != nil {
		return 0, err
	}

	count := 0
	for _, row := range list {
		diff := row.DeltaBalance
		if math.Abs(diff) < reconcileEpsilon {
			continue
		}

		req := &invoice_iface.CreateBalanceLogRequest{
			ChangeType: invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_ADJUSTMENT,
			Note:       "reconcile legacy unpaid invoices",
		}
		if diff > 0 {
			// debt understated: accrue via the creditor's RECEIVABLE leg; its
			// mirror drives PAYABLE(from,to) down by diff.
			req.TeamId, req.ForTeamId = row.ForTeamID, row.TeamID
			req.BalanceType = invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE
			req.ChangeAmount = diff
		} else {
			// debt overstated: pay it down directly on PAYABLE(from,to).
			req.TeamId, req.ForTeamId = row.TeamID, row.ForTeamID
			req.BalanceType = invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE
			req.ChangeAmount = -diff
		}

		if _, err := s.CreateBalanceLog(ctx, connect.NewRequest(req)); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
