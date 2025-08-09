package main

import "C"

import (
	"goimpcircuit"
)

//export Calculate
func Calculate(code string, values []float64, freqs []float64) []complex128 {
	return goimpcore.Calculate(code, values, freqs)
}

func main() {}
