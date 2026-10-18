package reporting

import (
	"math"
	"math/big"
	"strconv"

	"github.com/894x/llm-test-studio/internal/domain"
)

func usesRoundedLatencySLA(results []domain.Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Dimensions[domain.SLAPrecisionDimension] != domain.SLALatencyTwoDecimalsV1 {
			// Unmarked historical results, including runs whose report was not yet
			// generated, must retain their original comparison semantics.
			return false
		}
	}
	return true
}

func roundLatencyMS(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	// Round the shortest decimal representation, as Intl.NumberFormat does in
	// the report UI. Multiplying binary floats by 100 misrounds values like 1.005.
	decimal, _ := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	decimal.Mul(decimal, big.NewRat(100, 1))
	cents, remainder := new(big.Int), new(big.Int)
	cents.QuoRem(decimal.Num(), decimal.Denom(), remainder)
	remainder.Abs(remainder).Lsh(remainder, 1)
	if remainder.Cmp(decimal.Denom()) >= 0 {
		cents.Add(cents, big.NewInt(int64(decimal.Sign())))
	}
	rounded, _ := new(big.Rat).SetFrac(cents, big.NewInt(100)).Float64()
	return rounded
}
