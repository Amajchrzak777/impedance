package goimpcore

import (
	"fmt"
	"github.com/maorshutman/lm"
	"gonum.org/v1/gonum/diff/fd"
	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/optimize"
	"log"
	"math"
	"sort"
	"strings"
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

// Status constants replacement for removed goimp status constants
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

func (s *Solver) problem(x []float64) float64 {
	calculated := CircuitImpedance(s.code, s.Freqs, x)
	return ChiSq(s.Observed, calculated, s.Weighting)
}

func (s *Solver) Solve(minFunc float64, maxIterations int) Result {
	if s.SmartMode == "eis" {
		return s.eisSolve(minFunc, maxIterations)
	} else if s.SmartMode == "gd" {
		return s.baseGDSolve()
	} else if s.SmartMode == "lm" {
		return s.lmSolve(minFunc, maxIterations)
	} else if s.SmartMode == "lbfgs" {
		return s.baseLBFGSSolve()
	} else if s.SmartMode == "newton" {
		return s.baseNewtonSolve()
	}
	return s.baseNMSolve()
}

// How Simplex works http://195.134.76.37/applets/AppletSimplex/Appl_Simplex2.html
func (s *Solver) baseNMSolve() Result {
	log.Println("base NM Solve Mode")
	
	// Check if InitValues is empty or nil
	if len(s.InitValues) == 0 {
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
	
	log.Printf("Using initial values: %v", s.InitValues)
	
	problem := optimize.Problem{
		Func: s.problem,
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
		Concurrent:        10000,
	}

	res, err := optimize.Minimize(problem, s.InitValues, settings, &optimize.NelderMead{})
	if err != nil {
		log.Printf("Nelder-Mead optimization failed: %v", err)
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
		Code:    s.code,
		Params:  res.X,
		Min:     res.F,
		MinUnit: "ChiSq",
		Payload: payload,
		Runtime: float64(res.Runtime / 1000),
		Status:  OK,
	}
}

func (s *Solver) baseLMSolve() Result {
	log.Println("Base LM Solve Mode")
	fnc := func(dst, x []float64) {
		calculated := CircuitImpedance(s.code, s.Freqs, x)
		if len(calculated) != len(s.Observed) {
			panic("solver: slice length mismatch")
		}
		for i, o := range s.Observed {
			c := calculated[i]
			d2 := math.Pow(o[0]-c[0], 2) + math.Pow(o[1]-c[1], 2)
			if s.Weighting == UNITY {
				dst[i] = math.Abs(d2)
			} else if s.Weighting == MODULUS {
				weight := math.Sqrt(math.Pow(o[0], 2) + math.Pow(o[1], 2))
				dst[i] = math.Abs(d2) / math.Pow(weight, 2)
			}
		}
	}

	jac := lm.NumJac{Func: fnc}

	problem := lm.LMProblem{
		Dim:        len(s.InitValues),
		Size:       len(s.Observed),
		Func:       fnc,
		Jac:        jac.Jac,
		InitParams: s.InitValues,
		Tau:        1e-13,
		Eps1:       1e-8,
		Eps2:       1e-8,
	}

	// Recover from LM panics (e.g., singular matrix)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("LM optimization panicked: %v", r)
		}
	}()
	
	res, err := lm.LM(problem, &lm.Settings{Iterations: 1000000, ObjectiveTol: 1e-16})
	if err != nil {
		log.Printf("LM optimization failed: %v", err)
		return Result{
			Params:  []float64{},
			Min:     math.Inf(1),
			MinUnit: "ChiSq",
			Runtime: 0,
			Status:  "ERROR",
			Payload: nil,
		}
	}

	return Result{
		Params:  res.X,
		Min:     ChiSq(s.Observed, CircuitImpedance(s.code, s.Freqs, res.X), s.Weighting),
		MinUnit: "ChiSq",
		Runtime: 0,
		Status:  OK,
		Payload: nil,
	}
}

func (s *Solver) baseGDSolve() Result {
	log.Println("Base GD Solve Mode")
	// https://sbinet.github.io/posts/2017-10-09-intro-to-minimization/
	grad := func(grad, x []float64) {
		fd.Gradient(grad, s.problem, x, &fd.Settings{
			Formula:     fd.Formula{},
			Step:        0,
			OriginKnown: false,
			OriginValue: 0,
			Concurrent:  false,
		})
	}

	hess := func(h *mat.SymDense, x []float64) {
		fd.Hessian(h, s.problem, x, nil)
	}

	status := func() (optimize.Status, error) {
		return 0, nil
	}

	problem := optimize.Problem{
		Func:   s.problem,
		Grad:   grad,
		Hess:   hess,
		Status: status,
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
		Concurrent:        10000,
	}

	res, err := optimize.Minimize(problem, s.InitValues, settings, &optimize.GradientDescent{})
	if err != nil {
		panic(err)
	}

	payload := map[string]interface{}{
		"majorIterations": res.MajorIterations,
		"funcEvaluations": res.FuncEvaluations,
	}

	return Result{
		Params:  res.X,
		Min:     res.F,
		MinUnit: "ChiSq",
		Runtime: float64(res.Runtime / 1000),
		Status:  OK,
		Payload: payload,
	}
}

func (s *Solver) eisSolve(minFunc float64, maxIterations int) Result {
	log.Println("EIS Solve Mode")

	// normalizes the input impedance data so that it is in the range [0, 1]
	scaleCoef := prepareData(&s.Observed)

	if len(s.InitValues) == 0 {
		s.InitValues = s.findInitValues(s.Freqs, s.Observed)
	}

	fmt.Println("InitValues:", s.InitValues)

	var (
		lastMin    = math.Inf(1)
		lastValues = make([]float64, len(s.InitValues))
		bestRes    = Result{Min: math.Inf(1)}
	)

	primaryValues := s.InitValues
	iterations := 0
	elements := GetElements(s.code)
	log.Println("elements:", elements)

	for iterations < maxIterations {
		res := s.baseNMSolve()
		log.Println("init:", s.InitValues)
		log.Println("resl:", res)

		if res.Min < bestRes.Min {
			bestRes = res
		}

		log.Println("iter:", iterations, "res:", res.Min, "bestRes", bestRes.Min)

		if res.Min < minFunc {
			break
		} else {
			s.InitValues = modifyParams(res.Params, res.Min > lastMin, primaryValues, lastValues, elements)
		}
		lastMin = res.Min
		lastValues = res.Params
		iterations++
	}

	scaleParams(&bestRes.Params, elements, scaleCoef)
	scaleData(&s.Observed, scaleCoef)

	return bestRes
}

func (s *Solver) lmSolve(minFunc float64, maxIterations int) Result {
	log.Println("LM Solve Mode")

	if len(s.InitValues) == 0 {
		s.InitValues = s.findInitValues(s.Freqs, s.Observed)
	}

	var (
		lastMin    = math.Inf(1)
		lastValues = make([]float64, len(s.InitValues))
		bestRes    = Result{Min: math.Inf(1)}
	)

	primaryInitValues := s.InitValues
	iterations := 0

	for iterations < maxIterations {
		res := s.baseLMSolve()

		if res.Min < bestRes.Min {
			bestRes = res
		}

		log.Println("iter:", iterations, "res:", res.Min, "bestRes", bestRes.Min)

		if res.Min < minFunc {
			break
		} else {
			s.InitValues = modifyParams(res.Params, res.Min > lastMin, primaryInitValues, lastValues, GetElements(s.code))
		}
		lastMin = res.Min
		lastValues = res.Params
		iterations++
	}
	return bestRes
}

func prepareData(impData *[][2]float64) float64 {
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

func (s *Solver) findInitValues(freqs []float64, impData [][2]float64) []float64 {
	initValues := make([]float64, 0)

	for _, char := range s.code {
		switch char {
		case 114: // R
			min, max := minMax(freqs)
			freqAver := math.Pow(10, (math.Log10(min)+math.Log10(max))/2)
			initValues = append(initValues, impData[findClosest(freqs, freqAver)][0])
		case 99: // C
			initValues = append(initValues, 1e-5)
		case 108: // L
			initValues = append(initValues, 1e-5)
		case 119: // W (Infinite Warburg)
			initValues = append(initValues, 1e-5)
		case 113: // Q (CPE)
			initValues = append(initValues, 1e-5)
			initValues = append(initValues, 0.8)
		case 111: // O (FLW Finite Length Warburg) first parameter Y0, second B
			initValues = append(initValues, 1)
			initValues = append(initValues, 1)
		case 116: // T (FSW Finite Space Warburg) first parameter Y0, second B
			initValues = append(initValues, 1)
			initValues = append(initValues, 1)
		case 103: // G (Gerischer) first parameter Y0, second k
			initValues = append(initValues, 1)
			initValues = append(initValues, 1)
		case 102: // F (Fractal Gerischer) first parameter Y0, second k, third a
			initValues = append(initValues, 1)
			initValues = append(initValues, 1)
			initValues = append(initValues, 1)
		}
	}
	log.Println(initValues)
	return initValues
}

func minMax(a []float64) (float64, float64) {
	if len(a) < 1 {
		return 0, 0
	}
	sort.Float64s(a)
	return a[0], a[len(a)-1]
}

func findClosest(a []float64, x float64) int {
	abs := math.Inf(1)
	index := 0
	for i, n := range a {
		absCurr := math.Abs(n - x)
		if absCurr < abs {
			abs = absCurr
			index = i
		}
	}
	return index
}

func modifyParams(values []float64, diff bool, primaryValues []float64, lastValues []float64, elements []string) []float64 {
	for i, n := range values {
		if n < 0 {
			values[i] = primaryValues[i]
		}
		//if diff {
		//	values[i] = n - primaryValues[i]*0.1
		//} else {
		//	values[i] = n + primaryValues[i]*0.1
		//}

		//if elements[i] == "r" && ((n >= 1000000) || (n < 0)) {
		//	values[i] = primaryValues[i]
		//}

		if elements[i] == "qn" && (n > 1) {
			values[i] = 1
		}

		if elements[i] == "qn" && (n < 0) {
			values[i] = 0
		}

		switch elements[i] {
		case "r", "c", "qy":
			values[i] = values[i] * 1.1
		}
		//if (elements[i] == "c" || elements[i] == "qy") && n > 1e-3 {
		//	values[i] = 1e-5
		//}
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

func GetModulo(data [][2]float64) []float64 {
	var res []float64
	for _, v := range data {
		res = append(res, math.Sqrt(math.Pow(v[0], 2)+math.Pow(v[1], 2)))
	}
	return res
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
		case "r", "qn", "ob", "ty", "tb":
			(*params)[i] = (*params)[i] * scale
		case "c", "w", "qy", "oy":
			(*params)[i] = (*params)[i] * 1 / scale
		}
	}
}

func scaleData(impData *[][2]float64, scale float64) {
	for i, v := range *impData {
		(*impData)[i] = [2]float64{v[0] * scale, v[1] * scale}
	}
}

func (s *Solver) baseLBFGSSolve() Result {
	log.Println("Base LBFGS Solve Mode")
	grad := func(grad, x []float64) {
		fd.Gradient(grad, s.problem, x, &fd.Settings{
			Formula:     fd.Formula{},
			Step:        0,
			OriginKnown: false,
			OriginValue: 0,
			Concurrent:  false,
		})
	}

	status := func() (optimize.Status, error) {
		return 0, nil
	}

	problem := optimize.Problem{
		Func:   s.problem,
		Grad:   grad,
		Status: status,
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
		Concurrent:        10000,
	}

	res, err := optimize.Minimize(problem, s.InitValues, settings, &optimize.LBFGS{})
	if err != nil {
		log.Printf("LBFGS optimization error: %v", err)
		return Result{Min: math.Inf(1), Status: "ERROR"}
	}

	payload := map[string]interface{}{
		"majorIterations": res.MajorIterations,
		"funcEvaluations": res.FuncEvaluations,
	}

	return Result{
		Params:  res.X,
		Min:     res.F,
		MinUnit: "ChiSq",
		Runtime: float64(res.Runtime / 1000),
		Status:  OK,
		Payload: payload,
	}
}

func (s *Solver) baseNewtonSolve() Result {
	log.Println("Base Newton Solve Mode")
	grad := func(grad, x []float64) {
		fd.Gradient(grad, s.problem, x, &fd.Settings{
			Formula:     fd.Formula{},
			Step:        0,
			OriginKnown: false,
			OriginValue: 0,
			Concurrent:  false,
		})
	}

	hess := func(h *mat.SymDense, x []float64) {
		fd.Hessian(h, s.problem, x, nil)
	}

	status := func() (optimize.Status, error) {
		return 0, nil
	}

	problem := optimize.Problem{
		Func:   s.problem,
		Grad:   grad,
		Hess:   hess,
		Status: status,
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
		Concurrent:        10000,
	}

	res, err := optimize.Minimize(problem, s.InitValues, settings, &optimize.Newton{})
	if err != nil {
		log.Printf("Newton optimization error: %v", err)
		return Result{Min: math.Inf(1), Status: "ERROR"}
	}

	payload := map[string]interface{}{
		"majorIterations": res.MajorIterations,
		"funcEvaluations": res.FuncEvaluations,
	}

	return Result{
		Params:  res.X,
		Min:     res.F,
		MinUnit: "ChiSq",
		Runtime: float64(res.Runtime / 1000),
		Status:  OK,
		Payload: payload,
	}
}

func (s *Solver) Clone() *Solver {
	newS := *s
	newS.Observed = make([][2]float64, len(s.Observed))
	copy(newS.Observed, s.Observed)

	newS.Freqs = make([]float64, len(s.Freqs))
	copy(newS.Freqs, s.Freqs)

	newS.InitValues = make([]float64, len(s.InitValues))
	copy(newS.InitValues, s.InitValues)

	return &newS
}
