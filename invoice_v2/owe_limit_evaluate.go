package invoice_v2

import (
	"github.com/pdcgo/invoice_service/invoice_models"
	"github.com/pdcgo/shared/db_models"
	"gorm.io/gorm"
)

// OweLimitEval is one resolved owe-limit relationship between a creditor and a
// debtor: the threshold in force (a row custom to the pair beats the creditor's
// default rule), the debtor's current debt, and whether that debt is still under
// the limit.
//
// It deliberately carries more than [invoice_iface.OweLimitAllow], which
// EvaluateOweLimits returns for the order-create gate. A boolean gate can leave
// ActiveAmount at zero when no config applies and call an unset threshold 0; a
// list column cannot, because the reader cannot tell those apart from a real zero
// debt and a configured "unlimited". So ActiveAmount is ALWAYS populated here, and
// Configured reports whether any row applied at all.
type OweLimitEval struct {
	// Threshold is the resolved owe limit. 0 means UNLIMITED, not zero credit.
	// Meaningless when Configured is false.
	Threshold float64
	// ActiveAmount is the debtor's current debt to the creditor, always populated.
	ActiveAmount float64
	// Allow reports ActiveAmount < Threshold. True when unconfigured or unlimited.
	Allow bool
	// Configured is false when no owe_limit_configurations row applies to the pair.
	Configured bool
	// IsDefault reports that Threshold came from the creditor's default rule
	// rather than a row custom to this pair.
	IsDefault bool
}

// oweLimitRule is the config resolved for one counterparty before the debt is
// applied. A custom row wins over the default rule regardless of scan order.
type oweLimitRule struct {
	threshold float64
	isDefault bool
	hasCustom bool
	hasCfg    bool
}

// EvaluateOweLimitsAsCreditor resolves, for one creditor, the owe limit it grants
// each of the given debtors — "how much may each of them owe me before I stop
// extending credit". The result is keyed by debtor team id.
func EvaluateOweLimitsAsCreditor(
	db *gorm.DB,
	creditorTeamID uint64,
	debtorTeamIDs []uint64,
) (map[uint64]*OweLimitEval, error) {
	if len(debtorTeamIDs) == 0 {
		return map[uint64]*OweLimitEval{}, nil
	}

	// Config: the creditor's own rows — its default rule plus any row custom to
	// one of these debtors.
	var cfgs []db_models.OweLimitConfiguration
	err := db.
		Model(&db_models.OweLimitConfiguration{}).
		Where("team_id = ?", creditorTeamID).
		Where("is_default = ? OR for_team_id IN ?", true, debtorTeamIDs).
		Find(&cfgs).
		Error
	if err != nil {
		return nil, err
	}

	rules := map[uint64]*oweLimitRule{}
	for i := range cfgs {
		cfg := cfgs[i]
		if cfg.IsDefault {
			// The default rule applies to every debtor that has no custom row.
			for _, d := range debtorTeamIDs {
				applyDefaultRule(rules, d, cfg.Threshold)
			}
			continue
		}
		if cfg.ForTeamID == nil {
			continue
		}
		applyCustomRule(rules, *cfg.ForTeamID, cfg.Threshold)
	}

	// Debt: each debtor's PAYABLE row pointing at this creditor.
	var balances []invoice_models.TeamBalance
	err = db.
		Model(&invoice_models.TeamBalance{}).
		Where("team_id IN ?", debtorTeamIDs).
		Where("for_team_id = ?", creditorTeamID).
		Where("balance_type = ?", btPayable).
		Find(&balances).
		Error
	if err != nil {
		return nil, err
	}

	debtOf := map[uint64]float64{}
	for _, b := range balances {
		debtOf[b.TeamID] = -b.Balance // PAYABLE is stored negative
	}

	return resolveOweLimits(debtorTeamIDs, rules, debtOf), nil
}

// EvaluateOweLimitsAsDebtor resolves, for one debtor, the owe limit each of the
// given creditors imposes on it — "how much may I owe each of them before they
// stop extending credit". The result is keyed by creditor team id.
//
// This is the same relation the CheckOweLimit gate evaluates; it is reimplemented
// here rather than reusing EvaluateOweLimits so the list gets the fully populated
// OweLimitEval. EvaluateOweLimits is left untouched — selling_service's v3
// OrderCreate gate depends on its exact current behaviour.
func EvaluateOweLimitsAsDebtor(
	db *gorm.DB,
	debtorTeamID uint64,
	creditorTeamIDs []uint64,
) (map[uint64]*OweLimitEval, error) {
	if len(creditorTeamIDs) == 0 {
		return map[uint64]*OweLimitEval{}, nil
	}

	// Config: each creditor's default rule, plus any row custom to this debtor.
	var cfgs []db_models.OweLimitConfiguration
	err := db.
		Model(&db_models.OweLimitConfiguration{}).
		Where("team_id IN ?", creditorTeamIDs).
		Where("is_default = ? OR for_team_id = ?", true, debtorTeamID).
		Find(&cfgs).
		Error
	if err != nil {
		return nil, err
	}

	rules := map[uint64]*oweLimitRule{}
	for i := range cfgs {
		cfg := cfgs[i]
		isCustom := !cfg.IsDefault && cfg.ForTeamID != nil && *cfg.ForTeamID == debtorTeamID
		if isCustom {
			applyCustomRule(rules, cfg.TeamID, cfg.Threshold)
			continue
		}
		if cfg.IsDefault {
			applyDefaultRule(rules, cfg.TeamID, cfg.Threshold)
		}
	}

	// Debt: this debtor's PAYABLE rows pointing at each creditor.
	var balances []invoice_models.TeamBalance
	err = db.
		Model(&invoice_models.TeamBalance{}).
		Where("team_id = ?", debtorTeamID).
		Where("for_team_id IN ?", creditorTeamIDs).
		Where("balance_type = ?", btPayable).
		Find(&balances).
		Error
	if err != nil {
		return nil, err
	}

	debtOf := map[uint64]float64{}
	for _, b := range balances {
		debtOf[b.ForTeamID] = -b.Balance // PAYABLE is stored negative
	}

	return resolveOweLimits(creditorTeamIDs, rules, debtOf), nil
}

// applyCustomRule records a row custom to the pair. It always wins over a default.
func applyCustomRule(rules map[uint64]*oweLimitRule, counterpartyID uint64, threshold float64) {
	r := ruleFor(rules, counterpartyID)
	r.threshold = threshold
	r.isDefault = false
	r.hasCustom = true
	r.hasCfg = true
}

// applyDefaultRule records the creditor's default rule, which a custom row for the
// pair overrides — whichever order the two rows are scanned in.
func applyDefaultRule(rules map[uint64]*oweLimitRule, counterpartyID uint64, threshold float64) {
	r := ruleFor(rules, counterpartyID)
	if r.hasCustom {
		return
	}
	r.threshold = threshold
	r.isDefault = true
	r.hasCfg = true
}

func ruleFor(rules map[uint64]*oweLimitRule, counterpartyID uint64) *oweLimitRule {
	r := rules[counterpartyID]
	if r == nil {
		r = &oweLimitRule{}
		rules[counterpartyID] = r
	}
	return r
}

// resolveOweLimits folds the config rules and the debts into one entry per
// counterparty. Counterparties with no config are reported as unconfigured and
// allowed, but still carry their real debt.
func resolveOweLimits(
	counterpartyIDs []uint64,
	rules map[uint64]*oweLimitRule,
	debtOf map[uint64]float64,
) map[uint64]*OweLimitEval {
	out := make(map[uint64]*OweLimitEval, len(counterpartyIDs))
	for _, id := range counterpartyIDs {
		eval := &OweLimitEval{
			Allow:        true, // no config => allow
			ActiveAmount: debtOf[id],
		}
		out[id] = eval

		r := rules[id]
		if r == nil || !r.hasCfg {
			continue
		}
		eval.Configured = true
		eval.IsDefault = r.isDefault
		eval.Threshold = r.threshold
		if r.threshold == 0 {
			continue // 0 => unlimited
		}
		eval.Allow = eval.ActiveAmount < r.threshold // strict: debt at the limit blocks
	}
	return out
}
