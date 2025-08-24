package webhook

import (
	"log"
	"math"
	"math/cmplx"

	"github.com/kacperjurak/goimpcore/pkg/models"
)

// Calculator handles impedance calculations for circuit elements
type Calculator struct{}

// NewCalculator creates a new impedance calculator
func NewCalculator() *Calculator {
	return &Calculator{}
}

// CalculateElementImpedances calculates impedance for each circuit element
func (c *Calculator) CalculateElementImpedances(frequencies []float64, parameters []float64, elementNames []string) []models.ElementImpedance {
	var result []models.ElementImpedance

	for i, elementName := range elementNames {
		if i >= len(parameters) {
			break
		}

		impedances := c.calculateImpedanceForElement(elementName, parameters[i], frequencies, parameters, elementNames, i)

		// Only add element if we calculated impedances (skip qy parameter)
		if len(impedances) > 0 {
			displayName := c.getDisplayName(elementName)
			result = append(result, models.ElementImpedance{
				Name:       displayName,
				Impedances: impedances,
			})
		}
	}

	return result
}

// calculateImpedanceForElement calculates impedance for a specific element
func (c *Calculator) calculateImpedanceForElement(elementName string, parameter float64, frequencies []float64, parameters []float64, elementNames []string, index int) []map[string]float64 {
	var impedances []map[string]float64

	for _, freq := range frequencies {
		w := 2 * math.Pi * freq
		impedance := c.calculateElementImpedance(elementName, parameter, w, parameters, elementNames, index)

		// Sanitize impedance values for JSON compatibility
		realPart, imagPart := c.sanitizeImpedance(impedance, elementName, freq)

		impedances = append(impedances, map[string]float64{
			"real": realPart,
			"imag": imagPart,
		})
	}

	return impedances
}

// calculateElementImpedance calculates impedance based on element type
func (c *Calculator) calculateElementImpedance(elementName string, parameter float64, w float64, parameters []float64, elementNames []string, index int) complex128 {
	switch elementName {
	case "r": // Resistance
		return complex(parameter, 0)

	case "c": // Capacitance
		if parameter != 0 {
			return complex(1, 0) / (complex(0, 1) * complex(w, 0) * complex(parameter, 0))
		}

	case "l": // Inductance
		return complex(0, 1) * complex(w, 0) * complex(parameter, 0)

	case "w": // Warburg
		if parameter != 0 {
			sqrt_jw := complex(math.Sqrt(w/2), math.Sqrt(w/2))
			return complex(1, 0) / (complex(parameter, 0) * sqrt_jw)
		}

	case "qy": // CPE Y parameter - skip, will be combined with qn
		return complex(0, 0)

	case "qn": // CPE n parameter - calculate full CPE impedance
		if index > 0 && elementNames[index-1] == "qy" {
			qY := parameters[index-1] // Previous parameter is Y0
			qN := parameter           // Current parameter is n
			if qY != 0 {
				// Z_CPE = 1 / (Q * (jω)^n)
				jwPowN := cmplx.Pow(complex(0, w), complex(qN, 0))
				return complex(1, 0) / (complex(qY, 0) * jwPowN)
			}
		}

	default:
		// Handle other element types by first character
		return c.calculateByFirstChar(elementName, parameter, w)
	}

	return complex(0, 0)
}

// calculateByFirstChar handles element calculation by first character
func (c *Calculator) calculateByFirstChar(elementName string, parameter float64, w float64) complex128 {
	if len(elementName) == 0 {
		return complex(0, 0)
	}

	switch elementName[0] {
	case 'r':
		return complex(parameter, 0)
	case 'c':
		if parameter != 0 {
			return complex(1, 0) / (complex(0, 1) * complex(w, 0) * complex(parameter, 0))
		}
	case 'l':
		return complex(0, 1) * complex(w, 0) * complex(parameter, 0)
	case 'w':
		if parameter != 0 {
			sqrt_jw := complex(math.Sqrt(w/2), math.Sqrt(w/2))
			return complex(1, 0) / (complex(parameter, 0) * sqrt_jw)
		}
	}

	return complex(0, 0)
}

// sanitizeImpedance handles NaN, Inf values for JSON compatibility
func (c *Calculator) sanitizeImpedance(impedance complex128, elementName string, freq float64) (float64, float64) {
	realPart := real(impedance)
	imagPart := imag(impedance)

	if math.IsNaN(realPart) || math.IsInf(realPart, 0) {
		log.Printf("Warning: Invalid real impedance (%v) for element %s at freq %.2f Hz, setting to 0.0", realPart, elementName, freq)
		realPart = 0.0
	}
	if math.IsNaN(imagPart) || math.IsInf(imagPart, 0) {
		log.Printf("Warning: Invalid imaginary impedance (%v) for element %s at freq %.2f Hz, setting to 0.0", imagPart, elementName, freq)
		imagPart = 0.0
	}

	return realPart, imagPart
}

// getDisplayName returns the display name for an element
func (c *Calculator) getDisplayName(elementName string) string {
	if elementName == "qn" {
		return "Q" // Show CPE as Q instead of qn
	}
	return elementName
}
