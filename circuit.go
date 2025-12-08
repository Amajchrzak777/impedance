package goimpcore

import (
	"math"
	"math/cmplx"
	"math/rand"
)

type mode int

const (
	SERIES mode = iota
	PARALLEL
)

func CircuitImpedance(code string, freqs []float64, values []float64) [][2]float64 {
	// Pre-allocate result slice to avoid repeated allocations during append
	res := make([][2]float64, 0, len(freqs))

	for _, freq := range freqs {
		var (
			mode           = SERIES
			stack          []complex128 // Let Go handle this allocation
			fromStack, tmp complex128   = 0, 0
			i              uint         = 0
			w                           = 2 * math.Pi * freq
		)
		for _, char := range code {
			switch char {
			case 40: // (
				stack = append(stack, tmp)
				tmp = 0
				changeMode(&mode)
				continue
			case 41: // )
				if stack == nil {
					panic("circuit: nil slice")
				}
				fromStack = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				changeMode(&mode)
				tmp = sum(tmp, fromStack, mode)
				continue
			case 114: // R
				tmp = sum(tmp, complex(values[i], 0), mode)
			case 99: // C
				tmp = sum(tmp, complex(1, 0)/(complex(0, 1)*complex(w, 0)*complex(values[i], 0)), mode)
			case 108: // L
				tmp = sum(tmp, complex(0, 1)*complex(w, 0)*complex(values[i], 0), mode)
			case 119: // W (Infinite Warburg)
				tmp = sum(tmp, complex(1, 0)/(cmplx.Sqrt(complex(0, 1)*complex(w, 0))*complex(values[i], 0)), mode)
			case 113: // Q (CPE)
				tmp = sum(tmp, complex(1, 0)/(cmplx.Pow(complex(0, 1)*complex(w, 0), complex(values[i+1], 0))*complex(values[i], 0)), mode)
				i++
			case 111: // O (FLW Finite Length Warburg) first parameter Y0, second B
				tanh := cmplx.Tanh(cmplx.Sqrt(complex(0, 1)*complex(w, 0)) * complex(values[i+1], 0))
				if cmplx.IsNaN(tanh) {
					tanh = complex(1, 0)
				}
				tmp = sum(tmp, tanh/(cmplx.Sqrt(complex(0, 1)*complex(w, 0))*complex(values[i], 0)), mode)
				i++
			case 116: // T (FSW Finite Space Warburg) first parameter Y0, second B
				coth := 1 / (cmplx.Tanh(cmplx.Sqrt(complex(0, 1)*complex(w, 0)) * complex(values[i+1], 0)))
				tmp = sum(tmp, coth/(cmplx.Sqrt(complex(0, 1)*complex(w, 0))*complex(values[i], 0)), mode)
				i++
			case 103: // G (Gerischer) first parameter Y0, second k
				tmp = sum(tmp, (cmplx.Pow(complex(values[i+1], 0)+(complex(0, 1)*complex(w, 0)), complex(-0.5, 0)))/complex(values[i], 0), mode)
				i++
			case 102: // F (Fractal Gerischer) first parameter Y0, second k, third a
				tmp = sum(tmp, (cmplx.Pow(complex(values[i+1], 0)+(complex(0, 1)*complex(w, 0)), complex(-values[i+2], 0)))/complex(values[i], 0), mode)
				i++
				i++
			}
			i++
		}

		tmpSlc := [2]float64{real(tmp), imag(tmp)}
		res = append(res, tmpSlc)
	}
	return res
}

func changeMode(mode *mode) {
	if *mode == SERIES {
		*mode = PARALLEL
	} else {
		*mode = SERIES
	}
}

func sum(z1 complex128, z2 complex128, mode mode) complex128 {
	var res complex128 = 0
	if mode == SERIES {
		res = z1 + z2
	} else {
		var (
			s1, s2 complex128 = 0, 0
		)
		if z1 == 0 {
			s1 = 0
		} else {
			s1 = 1 / z1
		}
		if z2 == 0 {
			s2 = 0
		} else {
			s2 = 1 / z2
		}
		res = 1 / (s1 + s2)
	}
	return res
}

func noise(v *[2]float64, nl float64) {
	zrMaxNoise := math.Abs(v[0]) * nl
	ziMaxNoise := math.Abs(v[1]) * nl

	zrMin := v[0] - zrMaxNoise
	zrMax := v[0] + zrMaxNoise
	ziMin := v[1] - ziMaxNoise
	ziMax := v[1] + ziMaxNoise

	v[0] = rand.Float64()*(zrMax-zrMin) + zrMin
	v[1] = rand.Float64()*(ziMax-ziMin) + ziMin
}
