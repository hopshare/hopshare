package service

import (
	"context"
	"fmt"
)

const (
	DefaultTimebankMinBalance      = -5
	DefaultTimebankMaxBalance      = 10
	DefaultTimebankStartingBalance = 5
)

const (
	timebankMinBalanceLowerBound = -10
	timebankMaxBalanceUpperBound = 20
	timebankStartBalanceUpper    = 10
)

type timebankPolicy struct {
	MinBalance      int
	MaxBalance      int
	StartingBalance int
}

func validateTimebankPolicy(p timebankPolicy) error {
	if p.MinBalance <= timebankMinBalanceLowerBound || p.MinBalance >= 0 {
		return ErrInvalidTimebankMinBalance
	}
	if p.MaxBalance <= 0 || p.MaxBalance >= timebankMaxBalanceUpperBound {
		return ErrInvalidTimebankMaxBalance
	}
	if p.StartingBalance <= 0 || p.StartingBalance >= timebankStartBalanceUpper {
		return ErrInvalidTimebankStart
	}
	if p.StartingBalance > p.MaxBalance {
		return ErrInvalidTimebank
	}
	return nil
}

func loadTimebankPolicy(ctx context.Context, q queryer, orgID int64) (timebankPolicy, error) {
	if orgID == 0 {
		return timebankPolicy{}, ErrMissingOrgID
	}

	var p timebankPolicy
	if err := q.QueryRowContext(ctx, `
		SELECT timebank_min_balance, timebank_max_balance, timebank_starting_balance
		FROM organizations
		WHERE id = $1
	`, orgID).Scan(&p.MinBalance, &p.MaxBalance, &p.StartingBalance); err != nil {
		return timebankPolicy{}, fmt.Errorf("load timebank policy: %w", err)
	}
	if err := validateTimebankPolicy(p); err != nil {
		return timebankPolicy{}, err
	}
	return p, nil
}
