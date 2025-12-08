package goimpcore

import (
	"context"
	"log"
	"math"
	"strings"
	"time"

	"github.com/maorshutman/lm"
	"gonum.org/v1/gonum/optimize"
)

type Weighting int

const (
	MODULUS Weighting = iota
	UNITY
)

// Result replacement for removed goimp.Result
type Result struct {
	Min      float64
	Params   []float64
	Status   string
	Solved   bool
	Iters    int
	FuncEval int
	Code     string
	MinUnit  string
	Payload  interface{}
	Runtime  float64
}

const (
	OK = "OK"
)

type Solver struct {
	code       string
	Freqs      []float64
	Observed   [][2]float64
	InitValues []float64
	SmartMode  string
	Weighting  Weighting
}

func NewSolver(code string, freqs []float64, observed [][2]float64) *Solver {
	return &Solver{strings.ToLower(code), freqs, observed, make([]float64, 0), "", MODULUS}
}

func (s *Solver) Solve(minFunc float64, maxIterations int) Result {
	if s == nil {
		return Result{}
	}

	switch s.SmartMode {
	case "eis":
		return s.eisSolve(minFunc, maxIterations)
	case "lm":
		return s.lmSolve(minFunc, maxIterations)
	default:
		return s.baseNMSolve()
	}
}

func modifyParams(values []float64, diff bool, primaryValues []float64, lastValues []float64, elements []string) []float64 {
	for i, n := range values {
		// Safety check: skip if element index is out of bounds
		if i >= len(elements) {
			continue
		}

		// Only fix clearly unphysical negative values
		if n < 0 {
			values[i] = primaryValues[i]
		}

		// Apply critical physical bounds for CPE exponent n (must be 0 < n < 1)
		if elements[i] == "qn" {
			if n < 0.1 {
				//log.Printf("INFO: CPE exponent n=%.6f too low, clamping to 0.1", n)
				values[i] = 0.1
			} else if n > 1.0 {
				//log.Printf("INFO: CPE exponent n=%.6f too high, clamping to 1.0", n)
				values[i] = 1.0
			} else {
				//log.Printf("DEBUG: CPE exponent n=%.6f (valid)", n)
			}
		}

		// Log extreme parameter values but don't reset them
		if elements[i] == "r" && n > 1e6 {
			//log.Printf("INFO: Resistance %.3e is very high", n)
		}

		if elements[i] == "qy" && (n < 1e-12 || n > 1e-2) {
			//log.Printf("INFO: CPE Y0 %.3e is outside typical range", n)
		}
	}
	return values
}

func ChiSq(observed, calculated [][2]float64, weighting Weighting) float64 {
	if len(observed) != len(calculated) {
		panic("solver chiSq: slice length mismatch")
	}
	chiSq := 0.0
	for i, o := range observed {
		c := calculated[i]
		d2 := math.Pow(o[0]-c[0], 2) + math.Pow(o[1]-c[1], 2)
		if weighting == UNITY {
			chiSq += d2
		} else if weighting == MODULUS {
			weight := math.Sqrt(math.Pow(o[0], 2) + math.Pow(o[1], 2))
			if weight > 0 {
				chiSq += d2 / math.Pow(weight, 2)
			} else {
				chiSq += d2
			}
		}
	}
	// Normalize by number of data points
	return chiSq / float64(len(observed))
}

func GetElements(code string) []string {
	var elements []string
	for _, char := range code {
		switch char {
		case 114, 99, 108, 119: // r, c, l ,w
			elements = append(elements, string(char))
		case 113: // Q
			elements = append(elements, "qy")
			elements = append(elements, "qn")
		case 111: // O
			elements = append(elements, "oy")
			elements = append(elements, "ob")
		case 116: // T
			elements = append(elements, "ty")
			elements = append(elements, "tb")
		case 103: // G
			elements = append(elements, "gy")
			elements = append(elements, "gk")
		case 102: // F
			elements = append(elements, "fy")
			elements = append(elements, "fk")
			elements = append(elements, "fa")
		}
	}
	return elements
}

func scaleParams(params *[]float64, elements []string, scale float64) {
	if len(*params) != len(elements) {
		panic("solver: slice length mismatch")
	}
	// TODO: implement "gy", "gk", "fy", "fk", "fa"
	for i, v := range elements {
		switch v {
		case "r":
			// Resistance scales with impedance
			(*params)[i] = (*params)[i] * scale
		case "c", "w", "qy", "oy":
			// Capacitance, Warburg, CPE Y0 scale inversely with impedance
			(*params)[i] = (*params)[i] * 1 / scale
		case "qn", "ob", "tb":
			// CPE exponent n, Warburg length parameters B are dimensionless - no scaling
			// Leave (*params)[i] unchanged
		case "ty":
			// Transmission line Y0 scales inversely like admittance
			(*params)[i] = (*params)[i] * 1 / scale
		}
	}
}

// estimateCapacitance from -Im(Z) vs log(f) slope at high frequencies
func (s *Solver) estimateCapacitance(freqs []float64, impData [][2]float64) float64 {
	if len(impData) < 5 {
		return 0
	}
	// Use high frequency data (last 30% of points)
	start := int(float64(len(impData)) * 0.7)
	if start < 1 {
		start = 1
	}

	// Linear regression on log(f) vs log(-Im(Z)) to find capacitive behavior
	n := len(impData) - start
	if n < 3 {
		return 0
	}

	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0
	validPoints := 0

	for i := start; i < len(impData); i++ {
		if impData[i][1] < -1e-12 && freqs[i] > 0 { // negative imaginary part and positive frequency
			x := math.Log10(freqs[i])
			y := math.Log10(-impData[i][1])
			sumX += x
			sumY += y
			sumXY += x * y
			sumX2 += x * x
			validPoints++
		}
	}

	if validPoints < 3 {
		return 0
	}

	// Linear regression slope
	slope := (float64(validPoints)*sumXY - sumX*sumY) / (float64(validPoints)*sumX2 - sumX*sumX)
	intercept := (sumY - slope*sumX) / float64(validPoints)

	// For capacitor: Z = 1/(2πfC), so log|Z| = -log(2πC) - log(f)
	// Slope should be close to -1 for pure capacitive behavior
	if slope < -0.7 && slope > -1.3 {
		// Calculate capacitance: C = 1/(2π * 10^intercept)
		capacitance := 1.0 / (2.0 * math.Pi * math.Pow(10, intercept))
		if capacitance > 1e-12 && capacitance < 1 { // reasonable capacitance range
			return capacitance
		}
	}
	return 0
}

// estimateInductance from Im(Z) vs f slope at high frequencies
func (s *Solver) estimateInductance(freqs []float64, impData [][2]float64) float64 {
	if len(impData) < 5 {
		return 0
	}
	// Look for inductive behavior at high frequencies
	start := int(float64(len(impData)) * 0.8)
	if start < 1 {
		start = 1
	}

	// Check if imaginary part increases with frequency (inductive behavior)
	positiveSlope := 0
	totalChecks := 0

	for i := start; i < len(impData)-1; i++ {
		if freqs[i+1] > freqs[i] {
			if impData[i+1][1] > impData[i][1] {
				positiveSlope++
			}
			totalChecks++
		}
	}

	if totalChecks > 0 && float64(positiveSlope)/float64(totalChecks) > 0.6 {
		// Estimate inductance from L = Im(Z)/(2πf) at highest frequency
		lastIdx := len(impData) - 1
		if impData[lastIdx][1] > 1e-12 && freqs[lastIdx] > 0 {
			inductance := impData[lastIdx][1] / (2.0 * math.Pi * freqs[lastIdx])
			if inductance > 1e-9 && inductance < 1 { // reasonable inductance range
				return inductance
			}
		}
	}
	return 0
}

// estimateWarburgCoefficient from low frequency Im(Z) vs f^(-0.5) relationship
func (s *Solver) estimateWarburgCoefficient(freqs []float64, impData [][2]float64) float64 {
	if len(impData) < 5 {
		return 0
	}
	// Use low frequency data (first 30% of points)
	end := int(float64(len(impData)) * 0.3)
	if end < 3 {
		end = 3
	}
	if end > len(impData) {
		end = len(impData)
	}

	// Linear regression on f^(-0.5) vs Im(Z)
	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0
	validPoints := 0

	for i := 0; i < end; i++ {
		if freqs[i] > 0 && impData[i][1] != 0 {
			x := 1.0 / math.Sqrt(freqs[i]) // f^(-0.5)
			y := impData[i][1]             // Im(Z)
			sumX += x
			sumY += y
			sumXY += x * y
			sumX2 += x * x
			validPoints++
		}
	}

	if validPoints < 3 {
		return 0
	}

	// Linear regression slope
	slope := (float64(validPoints)*sumXY - sumX*sumY) / (float64(validPoints)*sumX2 - sumX*sumX)

	// For Warburg: Im(Z) = σ * ω^(-0.5), slope should be positive
	if slope > 0 {
		warburgCoeff := slope * math.Sqrt(2.0*math.Pi) // Convert to standard Warburg coefficient
		if warburgCoeff > 1e-6 && warburgCoeff < 1e3 { // reasonable range
			return warburgCoeff
		}
	}
	return 0
}

// estimateCPEParameters estimates Y0 and n from impedance magnitude and phase
func (s *Solver) estimateCPEParameters(freqs []float64, impData [][2]float64) [2]float64 {
	result := [2]float64{0, 0}
	if len(impData) < 5 {
		return result
	}

	// Use middle frequency range for CPE analysis
	start := int(float64(len(impData)) * 0.2)
	end := int(float64(len(impData)) * 0.8)
	if start < 1 {
		start = 1
	}
	if end > len(impData) {
		end = len(impData)
	}

	// Calculate magnitude and phase
	sumLogF, sumLogZ, sumLogFLogZ, sumLogF2 := 0.0, 0.0, 0.0, 0.0
	sumPhase := 0.0
	validPoints := 0

	for i := start; i < end; i++ {
		if freqs[i] > 0 {
			magnitude := math.Sqrt(impData[i][0]*impData[i][0] + impData[i][1]*impData[i][1])
			if magnitude > 1e-12 {
				logF := math.Log10(freqs[i])
				logZ := math.Log10(magnitude)
				phase := math.Atan2(impData[i][1], impData[i][0])

				sumLogF += logF
				sumLogZ += logZ
				sumLogFLogZ += logF * logZ
				sumLogF2 += logF * logF
				sumPhase += phase
				validPoints++
			}
		}
	}

	if validPoints < 3 {
		return result
	}

	// Linear regression on log(f) vs log|Z| to find n
	slope := (float64(validPoints)*sumLogFLogZ - sumLogF*sumLogZ) / (float64(validPoints)*sumLogF2 - sumLogF*sumLogF)
	intercept := (sumLogZ - slope*sumLogF) / float64(validPoints)
	avgPhase := sumPhase / float64(validPoints)

	// For CPE: |Z| = 1/(Y0 * ω^n), so log|Z| = -log(Y0) - n*log(ω)
	// Slope = -n, so n = -slope
	n := -slope
	if n > 0.1 && n < 1.0 {
		result[1] = n
		// Calculate Y0 from intercept
		y0 := 1.0 / math.Pow(10, intercept)
		if y0 > 1e-12 && y0 < 1 {
			result[0] = y0
		}
	}

	// Validate with phase information
	// For CPE: phase = -n*π/2
	expectedPhase := -n * math.Pi / 2.0
	if math.Abs(avgPhase-expectedPhase) < math.Pi/4 { // within reasonable tolerance
		return result
	}

	// If phase doesn't match, try to estimate n from phase
	if avgPhase < -0.1 && avgPhase > -1.4 { // reasonable phase range
		nFromPhase := -2.0 * avgPhase / math.Pi
		if nFromPhase > 0.1 && nFromPhase < 1.0 {
			result[1] = nFromPhase
		}
	}

	return result
}

// baseNMSolve is a thread-safe version that works with local copies
func (s *Solver) baseNMSolve() Result {
	// Work with local copy of InitValues to avoid race conditions
	localInitValues := make([]float64, len(s.InitValues))
	copy(localInitValues, s.InitValues)

	// Check if InitValues is empty or nil
	if len(localInitValues) == 0 {
		log.Printf("ERROR: No initial values provided for optimization")
		return Result{
			Params:  []float64{},
			Min:     math.Inf(1),
			MinUnit: "ChiSq",
			Runtime: 0,
			Status:  "ERROR",
			Payload: nil,
		}
	}

	// Create local problem function that captures solver data immutably
	localFreqs := make([]float64, len(s.Freqs))
	copy(localFreqs, s.Freqs)
	localObserved := make([][2]float64, len(s.Observed))
	copy(localObserved, s.Observed)
	localCode := s.code
	localWeighting := s.Weighting

	problemFunc := func(x []float64) float64 {
		calculated := CircuitImpedance(localCode, localFreqs, x)
		chiSq := ChiSq(localObserved, calculated, localWeighting)

		// Add penalty for Qn parameters outside [0.1, 1.0]
		penalty := 0.0
		elements := GetElements(localCode)
		for i, elem := range elements {
			if elem == "qn" && i < len(x) {
				if x[i] < 0.1 {
					penalty += 1e6 * math.Pow(0.1-x[i], 2)
				} else if x[i] > 1.0 {
					penalty += 1e6 * math.Pow(x[i]-1.0, 2)
				}
			}
		}
		return chiSq + penalty
	}

	problem := optimize.Problem{
		Func: problemFunc,
	}

	settings := &optimize.Settings{
		InitValues:        nil,
		GradientThreshold: 0,
		Converger:         nil,
		MajorIterations:   0,
		Runtime:           0,
		FuncEvaluations:   0,
		GradEvaluations:   0,
		HessEvaluations:   0,
		Recorder:          nil,
		Concurrent:        1, // Disable internal parallelism - we parallelize at worker level
	}

	// Add timeout to prevent hanging optimization
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Channel to capture the result
	type optimizeResult struct {
		result *optimize.Result
		err    error
	}
	resultChan := make(chan optimizeResult, 1)

	// Run optimization in goroutine with timeout
	go func() {
		res, err := optimize.Minimize(problem, localInitValues, settings, &optimize.NelderMead{})
		resultChan <- optimizeResult{result: res, err: err}
	}()

	// Wait for either completion or timeout
	var res *optimize.Result
	var err error
	select {
	case result := <-resultChan:
		res = result.result
		err = result.err
	case <-ctx.Done():
		return Result{
			Params:  []float64{},
			Min:     math.Inf(1),
			MinUnit: "ChiSq",
			Runtime: 30.0,
			Status:  "TIMEOUT",
			Payload: nil,
		}
	}

	if err != nil {
		return Result{
			Params:  []float64{},
			Min:     math.Inf(1),
			MinUnit: "ChiSq",
			Runtime: 0,
			Status:  "ERROR",
			Payload: nil,
		}
	}

	payload := map[string]interface{}{
		"majorIterations": res.MajorIterations,
		"funcEvaluations": res.FuncEvaluations,
	}

	return Result{
		Code:    localCode,
		Params:  res.X,
		Min:     res.F,
		MinUnit: "ChiSq",
		Payload: payload,
		Runtime: float64(res.Runtime / 1000),
		Status:  OK,
	}
}

// eisSolve is a thread-safe version that works with local copies
func (s *Solver) eisSolve(minFunc float64, maxIterations int) Result {
	// Add overall timeout for the entire EIS solve process
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	localObserved := make([][2]float64, len(s.Observed))
	copy(localObserved, s.Observed)

	localInitValues := make([]float64, len(s.InitValues))
	copy(localInitValues, s.InitValues)

	localFreqs := make([]float64, len(s.Freqs))
	copy(localFreqs, s.Freqs)

	// normalizes the input impedance data so that it is in the range [0, 1]
	scaleCoef := prepareDataLocal(&localObserved)

	if len(localInitValues) == 0 {
		localInitValues = s.findInitValuesLocal(localFreqs, localObserved)
	}

	var (
		lastMin    = math.Inf(1)
		lastValues = make([]float64, len(localInitValues))
		bestRes    = Result{Min: math.Inf(1)}
	)

	primaryValues := make([]float64, len(localInitValues))
	copy(primaryValues, localInitValues)
	iterations := 0
	elements := GetElements(s.code)
	for iterations < maxIterations {
		// Check for timeout
		select {
		case <-ctx.Done():
			if bestRes.Min != math.Inf(1) {
				scaleParams(&bestRes.Params, elements, scaleCoef)
				scaleDataLocal(&localObserved, scaleCoef)
				bestRes.Status = "TIMEOUT"
				return bestRes
			}
			return Result{
				Params:  []float64{},
				Min:     math.Inf(1),
				MinUnit: "ChiSq",
				Runtime: 60.0,
				Status:  "TIMEOUT",
				Payload: nil,
			}
		default:
		}

		tempSolver := &Solver{
			code:       s.code,
			Freqs:      localFreqs,
			Observed:   localObserved,
			InitValues: localInitValues,
			SmartMode:  s.SmartMode,
			Weighting:  s.Weighting,
		}

		res := tempSolver.baseNMSolve()

		if res.Min < bestRes.Min {
			bestRes = res
		}

		if res.Min < minFunc {
			break
		} else {
			localInitValues = modifyParams(res.Params, res.Min > lastMin, primaryValues, lastValues, elements)
		}
		lastMin = res.Min
		copy(lastValues, res.Params)
		iterations++
	}

	scaleParams(&bestRes.Params, elements, scaleCoef)
	scaleDataLocal(&localObserved, scaleCoef)

	return bestRes
}

// lmSolve is a thread-safe version that works with local copies
func (s *Solver) lmSolve(minFunc float64, maxIterations int) Result {
	localObserved := make([][2]float64, len(s.Observed))
	copy(localObserved, s.Observed)

	localInitValues := make([]float64, len(s.InitValues))
	copy(localInitValues, s.InitValues)

	localFreqs := make([]float64, len(s.Freqs))
	copy(localFreqs, s.Freqs)

	// Normalize the input impedance data to [0, 1] range for better convergence
	scaleCoef := prepareDataLocal(&localObserved)

	if len(localInitValues) == 0 {
		localInitValues = s.findInitValuesLocal(localFreqs, localObserved)
	}

	var (
		lastMin    = math.Inf(1)
		lastValues = make([]float64, len(localInitValues))
		bestRes    = Result{Min: math.Inf(1)}
	)

	primaryInitValues := make([]float64, len(localInitValues))
	copy(primaryInitValues, localInitValues)
	iterations := 0
	elements := GetElements(s.code)

	for iterations < maxIterations {
		// Create a temporary solver with current values for this iteration
		tempSolver := &Solver{
			code:       s.code,
			Freqs:      localFreqs,
			Observed:   localObserved,
			InitValues: localInitValues,
			SmartMode:  s.SmartMode,
			Weighting:  s.Weighting,
		}

		res := tempSolver.baseLMSolve()

		if res.Min < bestRes.Min {
			bestRes = res
		}

		if res.Min < minFunc {
			break
		} else {
			localInitValues = modifyParams(res.Params, res.Min > lastMin, primaryInitValues, lastValues, elements)
		}
		lastMin = res.Min
		copy(lastValues, res.Params)
		iterations++
	}

	if len(bestRes.Params) > 0 && len(bestRes.Params) == len(elements) {
		scaleParams(&bestRes.Params, elements, scaleCoef)
	}
	scaleDataLocal(&localObserved, scaleCoef)

	return bestRes
}

func (s *Solver) baseLMSolve() Result {
	localFreqs := make([]float64, len(s.Freqs))
	copy(localFreqs, s.Freqs)
	localObserved := make([][2]float64, len(s.Observed))
	copy(localObserved, s.Observed)
	localInitValues := make([]float64, len(s.InitValues))
	copy(localInitValues, s.InitValues)
	localCode := s.code
	localWeighting := s.Weighting

	fnc := func(dst, x []float64) {
		calculated := CircuitImpedance(localCode, localFreqs, x)
		if len(calculated) != len(localObserved) {
			panic("stateless solver: slice length mismatch")
		}

		// Apply CPE exponent constraints during optimization - penalty function for qn element 0<n<1
		elements := GetElements(localCode)
		penalty := 0.0
		for i, elem := range elements {
			if elem == "qn" && i < len(x) {
				if x[i] < 0.1 {
					penalty += 1e6 * math.Pow(0.1-x[i], 2)
				} else if x[i] > 1.0 {
					penalty += 1e6 * math.Pow(x[i]-1.0, 2)
				}
			}
		}

		// dst has size 2*len(localObserved): [real_0, imag_0, real_1, imag_1, ...]
		for i, o := range localObserved {
			c := calculated[i]

			// Calculate weight for this data point
			weight := 1.0
			if localWeighting == MODULUS {
				weight = math.Sqrt(math.Pow(o[0], 2) + math.Pow(o[1], 2))
				if weight == 0 {
					weight = 1.0
				}
			}

			// Compute weighted residuals for real and imaginary parts separately
			dst[2*i] = (o[0] - c[0]) / weight   // Real part residual
			dst[2*i+1] = (o[1] - c[1]) / weight // Imaginary part residual
		}

		// Apply penalty to residuals if CPE constraints are violated
		// Distribute penalty across all residuals as sqrt(penalty/numResiduals)
		if penalty > 0 {
			penaltyPerResidual := math.Sqrt(penalty / float64(2*len(localObserved)))
			for i := range dst {
				// Add penalty contribution to residual magnitude
				if dst[i] >= 0 {
					dst[i] += penaltyPerResidual
				} else {
					dst[i] -= penaltyPerResidual
				}
			}
		}
	}

	jac := lm.NumJac{Func: fnc}

	problem := lm.LMProblem{
		Dim:        len(localInitValues),
		Size:       2 * len(localObserved), // 2 residuals per data point (real + imaginary)
		Func:       fnc,
		Jac:        jac.Jac,
		InitParams: localInitValues,
		Tau:        1e-6, // Higher damping for faster convergence
		Eps1:       1e-6, // Relaxed gradient tolerance
		Eps2:       1e-6, // Relaxed parameter change tolerance
	}

	res, err := lm.LM(problem, &lm.Settings{
		Iterations:   1000, // Sufficient iterations for convergence
		ObjectiveTol: 1e-8, // Relaxed from 1e-16 for faster convergence
	})
	if err != nil {
		return Result{
			Params:  localInitValues, // Return init values on error for debugging
			Min:     math.Inf(1),
			MinUnit: "ChiSq",
			Runtime: 0,
			Status:  "ERROR",
			Payload: nil,
		}
	}

	finalChiSq := ChiSq(localObserved, CircuitImpedance(localCode, localFreqs, res.X), localWeighting)

	return Result{
		Params:  res.X,
		Min:     finalChiSq,
		MinUnit: "ChiSq",
		Runtime: 0,
		Status:  OK,
		Iters:   0,
		Payload: nil,
	}
}

// prepareDataLocal works on a local copy instead of modifying the receiver
func prepareDataLocal(impData *[][2]float64) float64 {
	maxZr := float64(0)
	// TODO: Think about negative elements
	for _, v := range *impData {
		if v[0] > maxZr {
			maxZr = v[0]
		}
	}
	for i, v := range *impData {
		(*impData)[i] = [2]float64{v[0] / maxZr, v[1] / maxZr}
	}
	return maxZr
}

func scaleDataLocal(impData *[][2]float64, scale float64) {
	for i, v := range *impData {
		(*impData)[i] = [2]float64{v[0] * scale, v[1] * scale}
	}
}

func (s *Solver) findInitValuesLocal(freqs []float64, impData [][2]float64) []float64 {
	initValues := make([]float64, 0)

	// Extract impedance characteristics from data
	highFreqR := s.estimateHighFrequencyResistanceLocal(impData)
	lowFreqR := s.estimateLowFrequencyResistanceLocal(impData)
	capacitance := s.estimateCapacitanceLocal(freqs, impData)
	inductance := s.estimateInductanceLocal(freqs, impData)
	warburgCoeff := s.estimateWarburgCoefficientLocal(freqs, impData)
	cpeParams := s.estimateCPEParametersLocal(freqs, impData)

	for _, char := range s.code {
		switch char {
		case 114: // R
			// Use high frequency intercept for series resistance or polarization resistance
			resistance := highFreqR
			if resistance <= 0 {
				resistance = lowFreqR
			}
			if resistance <= 0 {
				resistance = impData[len(impData)/2][0] // fallback to mid-frequency real part
			}
			initValues = append(initValues, resistance)
		case 99: // C
			// Use estimated capacitance from slope analysis
			if capacitance > 0 {
				initValues = append(initValues, capacitance)
			} else {
				initValues = append(initValues, 1e-5) // fallback
			}
		case 108: // L
			// Use estimated inductance from high frequency behavior
			if inductance > 0 {
				initValues = append(initValues, inductance)
			} else {
				initValues = append(initValues, 1e-6) // fallback
			}
		case 119: // W (Infinite Warburg)
			// Use estimated Warburg coefficient from low frequency slope
			if warburgCoeff > 0 {
				initValues = append(initValues, warburgCoeff)
			} else {
				initValues = append(initValues, 1e-3) // fallback
			}
		case 113: // Q (CPE)
			// Use estimated CPE parameters Y0 and n
			if cpeParams[0] > 0 {
				initValues = append(initValues, cpeParams[0]) // Y0
			} else {
				initValues = append(initValues, 1e-5) // fallback Y0
			}
			if cpeParams[1] > 0.1 && cpeParams[1] < 1.0 {
				initValues = append(initValues, cpeParams[1]) // n
			} else {
				initValues = append(initValues, 0.8) // fallback n
			}
		}
	}
	return initValues
}

// Local versions of estimation functions that work on provided data
func (s *Solver) estimateHighFrequencyResistanceLocal(impData [][2]float64) float64 {
	if len(impData) < 3 {
		return 0
	}
	start := int(float64(len(impData)) * 0.9)
	if start < 0 {
		start = 0
	}
	sum := 0.0
	count := 0
	for i := start; i < len(impData); i++ {
		sum += impData[i][0]
		count++
	}
	if count > 0 {
		return sum / float64(count)
	}
	return impData[len(impData)-1][0]
}

func (s *Solver) estimateLowFrequencyResistanceLocal(impData [][2]float64) float64 {
	if len(impData) < 3 {
		return 0
	}
	end := int(float64(len(impData)) * 0.1)
	if end < 1 {
		end = 1
	}
	sum := 0.0
	count := 0
	for i := 0; i < end; i++ {
		sum += impData[i][0]
		count++
	}
	if count > 0 {
		return sum / float64(count)
	}
	return impData[0][0]
}

func (s *Solver) estimateCapacitanceLocal(freqs []float64, impData [][2]float64) float64 {
	return s.estimateCapacitance(freqs, impData)
}

func (s *Solver) estimateInductanceLocal(freqs []float64, impData [][2]float64) float64 {
	return s.estimateInductance(freqs, impData)
}

func (s *Solver) estimateWarburgCoefficientLocal(freqs []float64, impData [][2]float64) float64 {
	return s.estimateWarburgCoefficient(freqs, impData)
}

func (s *Solver) estimateCPEParametersLocal(freqs []float64, impData [][2]float64) [2]float64 {
	return s.estimateCPEParameters(freqs, impData)
}
