package runs

import "fmt"

const DefaultCaseConcurrency uint32 = 4
const MaxCaseConcurrency uint32 = 8

// Zero is an omitted optional command setting, resolved before snapshot creation.
func resolveCaseConcurrency(requested uint32) (uint32, error) {
	if requested == 0 {
		return DefaultCaseConcurrency, nil
	}
	if requested > MaxCaseConcurrency {
		return 0, fmt.Errorf("%w: case concurrency must be between 1 and %d", ErrInvalid, MaxCaseConcurrency)
	}
	return requested, nil
}
