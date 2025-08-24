package processing

import (
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/kacperjurak/goimpcore"
	"github.com/kacperjurak/goimpcore/pkg/config"
)

const (
	minFunc       = 1.35e-2
	maxIterations = 10
)

// EISProcessor handles EIS data processing
type EISProcessor struct{}

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

	// Validate impedance data
	for i, imp := range impData {
		if len(imp) != 2 {
			return goimpcore.Result{}, fmt.Errorf("invalid impedance data format at index %d", i)
		}
	}

	log.Printf("🔥 REAL EIS: Processing %d frequency points with config: %+v", len(freqs), cfg)

	code := strings.ToLower(cfg.Code)

	if cfg.OptimMethod == "all" {
		return p.runAllOptimizationMethods(code, freqs, impData, cfg)
	}

	return p.runSingleOptimizationMethod(code, freqs, impData, cfg, cfg.OptimMethod)
}

func (p *EISProcessor) runSingleOptimizationMethod(code string, freqs []float64, impData [][2]float64, cfg *config.Config, method string) (goimpcore.Result, error) {
	solver := goimpcore.NewSolver(code, freqs, impData)

	// Use provided InitValues or generate automatic ones
	if len(cfg.InitValues) > 0 {
		solver.InitValues = []float64(cfg.InitValues)
		log.Printf("Using provided initial values: %v", solver.InitValues)
	} else {
		solver.InitValues = p.generateInitialValues(code)
		log.Printf("Using auto-generated initial values: %v", solver.InitValues)
	}

	if cfg.Unity {
		solver.Weighting = goimpcore.UNITY
	} else {
		solver.Weighting = goimpcore.MODULUS
	}

	// Set the solver method based on the optimization method
	switch method {
	case "nelder-mead":
		solver.SmartMode = "eis" // Use EIS smart mode for multi-try approach
	case "levenberg-marquardt", "lm":
		solver.SmartMode = "lm"
	case "gradient-descent", "gd":
		solver.SmartMode = "gd"
	case "lbfgs":
		solver.SmartMode = "lbfgs"
	case "newton":
		solver.SmartMode = "newton"
	default:
		log.Printf("Unknown optimization method '%s', using Nelder-Mead", method)
		solver.SmartMode = "eis"
	}

	log.Printf("Using optimization method: %s", method)

	// Time the optimization
	startTime := time.Now()
	res := solver.Solve(minFunc, maxIterations)
	duration := time.Since(startTime)

	// Ensure consistent chi-square calculation for all methods
	// Skip recalculation for EIS mode as it handles scaling internally
	if res.Status != "ERROR" && len(res.Params) > 0 && (res.MinUnit != "ChiSq" || method != "levenberg-marquardt") && cfg.SmartMode != "eis" {
		// Debug the recalculation process
		theoreticalImp := goimpcore.CircuitImpedance(code, freqs, res.Params)

		actualChiSq := goimpcore.ChiSq(impData, theoreticalImp, solver.Weighting)
		log.Printf("DEBUG: ChiSq calculation result: %v (weighting: %v)", actualChiSq, solver.Weighting)

		// Check if recalculation produces NaN
		if math.IsNaN(actualChiSq) || math.IsInf(actualChiSq, 0) {
			log.Printf("WARNING: Recalculated chi-square is invalid (%v), keeping original result.Min (%v)", actualChiSq, res.Min)
		} else {
			log.Printf("INFO: Using recalculated chi-square (%v) instead of original (%v)", actualChiSq, res.Min)
			res.Min = actualChiSq
			res.MinUnit = "ChiSq"
		}
	} else if cfg.SmartMode == "eis" {
		log.Printf("INFO: Skipping chi-square recalculation for EIS mode (scaling handled internally)")
	}

	if res.Status == "ERROR" {
		log.Printf("EIS processing FAILED - Method: %s, Status: %s", method, res.Status)
	} else {
		log.Printf("EIS processing completed - Method: %s, Chi-square: %.14e", method, res.Min)
	}

	if !cfg.Quiet {
		if res.Status == "ERROR" {
			log.Printf("Method: %s FAILED - Status=%s", method, res.Status)
		} else {
			log.Printf("Method: %s, Min=%.12e, Params=%v, Status=%s", method, res.Min, res.Params, res.Status)
		}
	}

	log.Printf("Processing time: %v", duration)
	return res, nil
}

func (p *EISProcessor) runAllOptimizationMethods(code string, freqs []float64, impData [][2]float64, cfg *config.Config) (goimpcore.Result, error) {
	methods := []string{"nelder-mead", "levenberg-marquardt", "gradient-descent", "lbfgs", "newton"}
	var bestResult goimpcore.Result
	bestChiSq := math.Inf(1)

	log.Printf("Running all optimization methods for comparison...")

	for _, method := range methods {
		log.Printf("Testing method: %s", method)
		result, err := p.runSingleOptimizationMethod(code, freqs, impData, cfg, method)
		if err != nil {
			continue
		}

		if result.Status != "ERROR" && result.Min < bestChiSq {
			bestResult = result
			bestChiSq = result.Min
			log.Printf("New best method: %s with chi-square: %.12e", method, result.Min)
		}
	}

	if bestResult.Status == "" {
		log.Printf("All methods failed")
		return goimpcore.Result{
			Status: "ERROR",
			Min:    math.Inf(1),
			Params: []float64{},
		}, fmt.Errorf("all optimization methods failed")
	}

	log.Printf("Best overall result: chi-square=%.12e", bestResult.Min)
	return bestResult, nil
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
	case "r(cr)(cr)":
		// R1, C1, R2, C2, R3 (5 parameters)
		return []float64{50.0, 1e-6, 100.0, 1e-6, 100.0}
	case "r(q(r(qr)))":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	case "r(q(r(q(r(qr)))))":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3, Q3_Y0, Q3_n, R4
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	default:
		// Generic fallback: assume 4 parameters for R(QR) since that's our default
		log.Printf("Warning: Unknown circuit code '%s', using R(QR) 4-parameter defaults", code)
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
