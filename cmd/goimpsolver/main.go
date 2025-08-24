package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"github.com/kacperjurak/goimpcore"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	minFunc       = 1.35e-2
	maxIterations = 10
)

func main() {
	config := new(Config)

	flag.StringVar(&config.Code, "c", "R(QR)", "Boukamp Circuit Description code")
	flag.StringVar(&config.File, "f", "ASTM0.txt", "Measurement data file")
	flag.Var(&config.InitValues, "v", "Parameters init values (array)")               // for better fit the EIS
	flag.UintVar(&config.CutLow, "b", 0, "Cut X of begining frequencies from a file") // am not using
	flag.UintVar(&config.CutHigh, "e", 0, "Cut X of ending frequencies from a file")  // am not using
	flag.BoolVar(&config.Unity, "unity", false, "Use Unity weighting intead Modulus") // UNITY problematic data more focused on small values
	flag.StringVar(&config.SmartMode, "m", "eis", "Smart mode")
	flag.StringVar(&config.OptimMethod, "optim", "nelder-mead", "Optimization method: nelder-mead, levenberg-marquardt, gradient-descent, lbfgs, newton, or all")
	flag.BoolVar(&config.Benchmark, "benchmark", false, "Enable benchmark mode with timing (saves to benchmark_results.csv)")
	flag.BoolVar(&config.Flip, "noflip", false, "Don't flip imaginary part on image")
	flag.BoolVar(&config.ImgOut, "imgout", false, "Image data to STDOUT")
	flag.BoolVar(&config.ImgSave, "imgsave", false, "Save image to file")
	flag.StringVar(&config.ImgPath, "imgpath", "eis.svg", "Path to generated image")
	flag.UintVar(&config.ImgDPI, "dpi", 96, "Image DPI")
	flag.UintVar(&config.ImgSize, "imgsize", 4, "Image size (inches)")
	flag.BoolVar(&config.Concurrency, "concurrency", false, "Use concurrency for calculations")
	flag.UintVar(&config.Jobs, "jobs", 10, "Number of how many times trigger the calculations")
	flag.UintVar(&config.Threads, "threads", 10, "Number of threads to use for calculations")
	flag.BoolVar(&config.HTTPServer, "http", false, "Start HTTP server on port 8080")
	flag.BoolVar(&config.Quiet, "q", false, "Quiet mode")
	flag.Parse()

	if config.HTTPServer {
		startHTTPServer(config)
		return
	}

	freqs, impData := parseFile(config.File)
	freqs = freqs[config.CutLow : len(freqs)-int(config.CutHigh)]
	impData = impData[config.CutLow : len(impData)-int(config.CutHigh)]

	result := processEISData(freqs, impData, config)
	log.Printf("Final result: %+v", result)
}

// processEISData function disabled due to goimp dependency removal
// func processEISData(freqs []float64, impData [][2]float64, cfg *Config) goimp.Result {
func processEISData(freqs []float64, impData [][2]float64, cfg *Config) goimpcore.Result {
	log.Printf("Processing %d frequency points with config: %+v", len(freqs), cfg)

	code := strings.ToLower(cfg.Code)

	if cfg.OptimMethod == "all" {
		return runAllOptimizationMethods(code, freqs, impData, cfg)
	}

	return runSingleOptimizationMethod(code, freqs, impData, cfg, cfg.OptimMethod)
}

func runSingleOptimizationMethod(code string, freqs []float64, impData [][2]float64, cfg *Config, method string) goimpcore.Result {
	s := goimpcore.NewSolver(code, freqs, impData)

	// Use provided InitValues or generate automatic ones
	if len(cfg.InitValues) > 0 {
		s.InitValues = []float64(cfg.InitValues)
		log.Printf("Using provided initial values: %v", s.InitValues)
	} else {
		s.InitValues = generateInitialValues(code)
		log.Printf("Using auto-generated initial values: %v", s.InitValues)
	}

	if cfg.Unity {
		s.Weighting = goimpcore.UNITY
	} else {
		s.Weighting = goimpcore.MODULUS
	}

	// Set the solver method based on the optimization method
	switch method {
	case "nelder-mead":
		s.SmartMode = "eis" // Use EIS smart mode for multi-try approach
	case "levenberg-marquardt", "lm":
		s.SmartMode = "lm"
	case "gradient-descent", "gd":
		s.SmartMode = "gd"
	case "lbfgs":
		s.SmartMode = "lbfgs"
	case "newton":
		s.SmartMode = "newton"
	default:
		log.Printf("Unknown optimization method '%s', using Nelder-Mead", method)
		s.SmartMode = "eis"
	}

	log.Printf("Using optimization method: %s", method)

	// Time the optimization
	startTime := time.Now()
	res := s.Solve(minFunc, maxIterations)
	duration := time.Since(startTime)

	// Ensure consistent chi-square calculation for all methods
	// Skip recalculation for EIS mode as it handles scaling internally
	if res.Status != "ERROR" && len(res.Params) > 0 && (res.MinUnit != "ChiSq" || method != "levenberg-marquardt") && cfg.SmartMode != "eis" {
		// Debug the recalculation process
		theoreticalImp := goimpcore.CircuitImpedance(code, freqs, res.Params)

		actualChiSq := goimpcore.ChiSq(impData, theoreticalImp, s.Weighting)
		log.Printf("DEBUG: ChiSq calculation result: %v (weighting: %v)", actualChiSq, s.Weighting)

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

	// Save benchmark data if enabled
	if cfg.Benchmark {
		description := generateBenchmarkDescription(method, code, s.InitValues, len(impData), cfg)
		saveBenchmarkResult(method, code, len(s.InitValues), len(impData), duration, res, description)
	}

	return res
}

func runAllOptimizationMethods(code string, freqs []float64, impData [][2]float64, cfg *Config) goimpcore.Result {
	methods := []string{"nelder-mead", "levenberg-marquardt", "gradient-descent", "lbfgs", "newton"}
	bestResult := goimpcore.Result{Min: 1e10} // Initialize with high value

	log.Println("Running all optimization methods for comparison:")
	log.Println(strings.Repeat("=", 60))

	for _, method := range methods {
		log.Printf("\n--- Testing %s ---", strings.ToUpper(method))
		result := runSingleOptimizationMethod(code, freqs, impData, cfg, method)

		if result.Status == "ERROR" {
			log.Printf("Method: %-20s | FAILED", method)
		} else {
			log.Printf("Method: %-20s | Chi-square: %.12e | Params: %v",
				method, result.Min, result.Params)

			if result.Min < bestResult.Min {
				bestResult = result
				bestResult.Code = method // Store the best method name
			}
		}
	}

	log.Println("\n" + strings.Repeat("=", 60))
	log.Printf("BEST METHOD: %s with Chi-square: %.12e", bestResult.Code, bestResult.Min)
	log.Println(strings.Repeat("=", 60))

	return bestResult
}

func parseFile(file string) (freqs []float64, impData [][2]float64) {
	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		var lineVals [3]float64

		for i := 0; i < 3; i++ {
			l := strings.Fields(line)
			val, err := strconv.ParseFloat(l[i], 64)
			if err != nil {
				log.Fatal(err)
			}
			lineVals[i] = val
		}
		//measData = append(measData, lineVals)
		freqs = append(freqs, lineVals[0])
		impData = append(impData, [2]float64{lineVals[1], lineVals[2]})
	}
	return freqs, impData
}

// generateBenchmarkDescription creates a descriptive label for the benchmark test
func generateBenchmarkDescription(method, circuit string, initValues []float64, dataPoints int, cfg *Config) string {
	description := ""

	// Analyze initial values quality
	initQuality := analyzeInitialGuessQuality(initValues)
	description += initQuality

	// Add data size context
	if dataPoints > 200 {
		description += ", Large dataset"
	} else if dataPoints < 150 {
		description += ", Small dataset"
	}

	// Add smart mode context
	switch cfg.SmartMode {
	case "eis":
		description += ", EIS smart mode (multi-try)"
	case "lm":
		description += ", Direct LM mode"
	case "gd":
		description += ", Direct GD mode"
	case "":
		description += ", Base optimization mode"
	default:
		description += ", " + cfg.SmartMode + " mode"
	}

	// Add circuit complexity context
	complexityLevel := getCircuitComplexityDescription(circuit)
	description += ", " + complexityLevel

	// Clean up leading comma/space
	if len(description) > 2 && description[:2] == ", " {
		description = description[2:]
	}

	return description
}

// analyzeInitialGuessQuality determines if initial values are good, bad, or terrible
func analyzeInitialGuessQuality(initValues []float64) string {
	if len(initValues) == 0 {
		return "No initial values"
	}

	// Check for extreme values that suggest bad guesses
	hasExtremeValues := false
	hasReasonableValues := true

	for _, val := range initValues {
		if val > 500 || val < 1e-10 || (val > 0.1 && val < 1e-6) {
			hasExtremeValues = true
		}
		if val < 0 || val > 10000 {
			hasReasonableValues = false
		}
	}

	if !hasReasonableValues {
		return "Terrible initial guesses"
	} else if hasExtremeValues {
		return "Poor initial guesses"
	} else {
		return "Good initial guesses"
	}
}

// getCircuitComplexityDescription returns a description of circuit complexity
func getCircuitComplexityDescription(circuit string) string {
	paramCount := 0
	// Count parameters by analyzing circuit elements
	for _, char := range circuit {
		switch char {
		case 'r', 'c', 'l', 'w': // Single parameter elements
			paramCount++
		case 'q': // CPE has 2 parameters
			paramCount += 2
		case 'o', 't', 'g': // Two parameter elements
			paramCount += 2
		case 'f': // Fractal has 3 parameters
			paramCount += 3
		}
	}

	if paramCount <= 3 {
		return fmt.Sprintf("Simple circuit (%d params)", paramCount)
	} else if paramCount <= 7 {
		return fmt.Sprintf("Medium complexity circuit (%d params)", paramCount)
	} else {
		return fmt.Sprintf("High complexity circuit (%d params)", paramCount)
	}
}

// saveBenchmarkResult saves timing and performance data to CSV
func saveBenchmarkResult(method, circuit string, params, dataPoints int, duration time.Duration, result goimpcore.Result, description string) {
	filename := "benchmark_results.csv"

	// Check if file exists to decide on header
	var writeHeader bool
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		writeHeader = true
	}

	// Open file for append
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening benchmark file: %v", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header if new file
	if writeHeader {
		header := []string{
			"Timestamp",
			"Method",
			"Circuit",
			"Parameters",
			"DataPoints",
			"Duration_ms",
			"ChiSquare",
			"Success",
			"Iterations",
			"FuncEvals",
			"Description",
		}
		if err := writer.Write(header); err != nil {
			log.Printf("Error writing benchmark header: %v", err)
			return
		}
	}

	// Extract additional info from result payload
	iterations := 0
	funcEvals := 0
	if result.Payload != nil {
		if payload, ok := result.Payload.(map[string]interface{}); ok {
			if iters, exists := payload["majorIterations"]; exists {
				if iterInt, ok := iters.(int); ok {
					iterations = iterInt
				}
			}
			if funcs, exists := payload["funcEvaluations"]; exists {
				if funcInt, ok := funcs.(int); ok {
					funcEvals = funcInt
				}
			}
		}
	}

	// Write benchmark record
	record := []string{
		time.Now().Format(time.RFC3339),
		method,
		circuit,
		strconv.Itoa(params),
		strconv.Itoa(dataPoints),
		fmt.Sprintf("%.6f", float64(duration.Nanoseconds())/1000000.0), // Convert to milliseconds
		fmt.Sprintf("%.12e", result.Min),
		strconv.FormatBool(result.Status == "OK"),
		strconv.Itoa(iterations),
		strconv.Itoa(funcEvals),
		description,
	}

	if err := writer.Write(record); err != nil {
		log.Printf("Error writing benchmark record: %v", err)
		return
	}

	log.Printf("📊 Benchmark: %s | %s | %d params | %.2f ms | Success: %v | %s",
		method, circuit, params, float64(duration.Nanoseconds())/1000000.0, result.Status == "OK", description)
}

// generateInitialValues creates reasonable default initial values for different circuit codes
func generateInitialValues(code string) []float64 {
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
		// Generic fallback: assume 7 parameters (medium complexity)
		log.Printf("Warning: Unknown circuit code '%s', using generic 7-parameter defaults", code)
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	}
}
