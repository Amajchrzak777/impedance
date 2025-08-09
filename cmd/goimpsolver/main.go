package main

import (
	"bufio"
	"flag"
	"github.com/kacperjurak/goimpcore"
	"log"
	"os"
	"strconv"
	"strings"
)

const (
	minFunc       = 1.35e-2
	maxIterations = 10
)

func main() {
	config := new(Config)

	flag.StringVar(&config.Code, "c", "R(CR)", "Boukamp Circuit Description code")
	flag.StringVar(&config.File, "f", "ASTM0.txt", "Measurement data file")
	flag.Var(&config.InitValues, "v", "Parameters init values (array)")
	flag.UintVar(&config.CutLow, "b", 0, "Cut X of begining frequencies from a file")
	flag.UintVar(&config.CutHigh, "e", 0, "Cut X of ending frequencies from a file")
	flag.BoolVar(&config.Unity, "unity", false, "Use Unity weighting intead Modulus")
	flag.StringVar(&config.SmartMode, "m", "eis", "Smart mode")
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
	s := goimpcore.NewSolver(code, freqs, impData)
	s.InitValues = []float64(cfg.InitValues)
	s.SmartMode = cfg.SmartMode
	
	if cfg.Unity {
		s.Weighting = goimpcore.UNITY
	} else {
		s.Weighting = goimpcore.MODULUS
	}

	res := s.Solve(minFunc, maxIterations)
	log.Printf("EIS processing completed - Chi-square: %.14e", res.Min)

	if !cfg.Quiet {
		log.Printf("Final result: Min=%.12e, Params=%v, Status=%s", res.Min, res.Params, res.Status)
	}

	return res
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
