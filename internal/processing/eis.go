package processing

import (
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/kacperjurak/goimpcore"
	"github.com/kacperjurak/goimpcore/pkg/config"
)

// EISProcessor handles EIS data processing
type EISProcessor struct {
}

// NewEISProcessor creates a new EIS processor
func NewEISProcessor() *EISProcessor {
	return &EISProcessor{}
}

// Process processes EIS data and returns the result
func (p *EISProcessor) Process(freqs []float64, impData [][2]float64, cfg *config.Config) (goimpcore.Result, error) {
	if len(freqs) == 0 {
		return goimpcore.Result{}, fmt.Errorf("no frequency data provided")
	}

	if len(impData) == 0 {
		return goimpcore.Result{}, fmt.Errorf("no impedance data provided")
	}

	if len(freqs) != len(impData) {
		return goimpcore.Result{}, fmt.Errorf("frequency and impedance data length mismatch: %d vs %d", len(freqs), len(impData))
	}

	code := strings.ToLower(cfg.Code)

	return p.runSingleOptimizationMethod(code, freqs, impData, cfg, cfg.OptimMethod)
}

func (p *EISProcessor) runSingleOptimizationMethod(code string, freqs []float64, impData [][2]float64, cfg *config.Config, method string) (goimpcore.Result, error) {
	solver := goimpcore.NewSolver(code, freqs, impData)

	if len(cfg.InitValues) > 0 {
		solver.InitValues = make([]float64, len(cfg.InitValues))
		copy(solver.InitValues, cfg.InitValues)
	} else {
		solver.InitValues = []float64{}
	}

	// Method-specific optimization parameters
	methodMinFunc := 1.35e-2
	methodMaxIters := 10

	if !cfg.Unity {
		solver.Weighting = goimpcore.MODULUS
	}

	switch method {
	case "nelder-mead", "nm":
		solver.SmartMode = "eis"
		methodMinFunc = 0.0135
		methodMaxIters = 10
	case "levenberg-marquardt", "lm":
		solver.SmartMode = "lm"
		methodMinFunc = 0.0135
		methodMaxIters = 10
	default:
		solver.SmartMode = "eis"
	}

	res := solver.Solve(methodMinFunc, methodMaxIters)

	if res.Status != "ERROR" && len(res.Params) > 0 && (res.MinUnit != "ChiSq" || method != "levenberg-marquardt") && cfg.SmartMode != "eis" {
		theoreticalImp := goimpcore.CircuitImpedance(code, freqs, res.Params)

		actualChiSq := goimpcore.ChiSq(impData, theoreticalImp, solver.Weighting)
		// Check if recalculation produces NaN
		if math.IsNaN(actualChiSq) || math.IsInf(actualChiSq, 0) {
		} else {
			res.Min = actualChiSq
			res.MinUnit = "ChiSq"
		}
	}

	return res, nil
}

// generateInitialValues creates reasonable default initial values for different circuit codes
func (p *EISProcessor) generateInitialValues(code string) []float64 {
	switch strings.ToLower(code) {
	case "r(cr)":
		// R1, C1, R2
		return []float64{50.0, 1e-6, 100.0}
	case "r(qr)":
		// R1, Q1_Y0, Q1_n, R2
		return []float64{50.0, 1e-6, 0.8, 100.0}
	case "r(qr)(qr)":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3 (7 parameters)
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	case "r(cr)(cr)":
		// R1, C1, R2, C2, R3 (5 parameters)
		return []float64{50.0, 1e-6, 100.0, 1e-6, 100.0}
	case "r(q(r(qr)))":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	case "r(q(r(q(r(qr)))))":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3, Q3_Y0, Q3_n, R4
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	case "r(rc(r(cr)))":
		// R1, R2, C1, R3, C2, R4 (6 parameters for R(RC(R(CR))))
		return []float64{50.0, 100.0, 1e-6, 100.0, 1e-6, 100.0}
	default:
		// Generic fallback: assume 4 parameters for R(QR) since that's our default
		return []float64{50.0, 1e-6, 0.8, 100.0}
	}
}

// ProcessorFunc creates a function compatible with the worker pool
func (p *EISProcessor) ProcessorFunc() func(freqs []float64, impData [][2]float64, config *config.Config) interface{} {
	return func(freqs []float64, impData [][2]float64, config *config.Config) interface{} {
		result, err := p.Process(freqs, impData, config)
		if err != nil {
			log.Printf("EIS processing error: %v", err)
			return goimpcore.Result{
				Status: "ERROR",
				Min:    0.0,
				Params: []float64{},
			}
		}
		return result
	}
}
