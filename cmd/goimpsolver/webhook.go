package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"math"
	"math/cmplx"
	"net/http"
	"time"
)

const webhookURL = "http://webplot:3001/webhook"

type ElementImpedance struct {
	Name       string               `json:"name"`
	Impedances []map[string]float64 `json:"impedances"`
}

type WebhookResponse struct {
	ID                 string             `json:"id"`
	Time               string             `json:"time"`
	ChiSquare          float64            `json:"chi_square"`
	RealImpedance      []float64          `json:"real_impedance"`
	ImaginaryImpedance []float64          `json:"imaginary_impedance"`
	Frequencies        []float64          `json:"frequencies"`
	Parameters         []float64          `json:"parameters"`
	ElementNames       []string           `json:"element_names"`
	ElementImpedances  []ElementImpedance `json:"element_impedances"`
	CircuitType        string             `json:"circuit_type"`
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// calculateElementImpedances calculates impedance for each circuit element
func calculateElementImpedances(frequencies []float64, parameters []float64, elementNames []string) []ElementImpedance {
	var result []ElementImpedance

	for i, elementName := range elementNames {
		if i >= len(parameters) {
			break
		}

		var impedances []map[string]float64
		for _, freq := range frequencies {
			w := 2 * math.Pi * freq
			var impedance complex128

			// Calculate impedance based on element type
			switch elementName {
			case "r": // Resistance
				impedance = complex(parameters[i], 0)
			case "c": // Capacitance
				if parameters[i] != 0 {
					impedance = complex(1, 0) / (complex(0, 1) * complex(w, 0) * complex(parameters[i], 0))
				}
			case "l": // Inductance
				impedance = complex(0, 1) * complex(w, 0) * complex(parameters[i], 0)
			case "w": // Warburg
				if parameters[i] != 0 {
					sqrt_jw := complex(math.Sqrt(w/2), math.Sqrt(w/2))
					impedance = complex(1, 0) / (complex(parameters[i], 0) * sqrt_jw)
				}
			case "qy": // CPE Y parameter - skip, will be combined with qn
				continue
			case "qn": // CPE n parameter - calculate full CPE impedance
				if i > 0 && elementNames[i-1] == "qy" {
					qY := parameters[i-1] // Previous parameter is Y0
					qN := parameters[i]   // Current parameter is n
					if qY != 0 {
						// Z_CPE = 1 / (Q * (jω)^n)
						jwPowN := cmplx.Pow(complex(0, w), complex(qN, 0))
						impedance = complex(1, 0) / (complex(qY, 0) * jwPowN)
					}
				}
			default:
				// Handle other element types by first character
				switch elementName[0] {
				case 'r':
					impedance = complex(parameters[i], 0)
				case 'c':
					if parameters[i] != 0 {
						impedance = complex(1, 0) / (complex(0, 1) * complex(w, 0) * complex(parameters[i], 0))
					}
				case 'l':
					impedance = complex(0, 1) * complex(w, 0) * complex(parameters[i], 0)
				case 'w':
					if parameters[i] != 0 {
						sqrt_jw := complex(math.Sqrt(w/2), math.Sqrt(w/2))
						impedance = complex(1, 0) / (complex(parameters[i], 0) * sqrt_jw)
					}
				default:
					impedance = complex(0, 0)
				}
			}

			// Handle NaN, Inf values for JSON compatibility
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

			impedances = append(impedances, map[string]float64{
				"real": realPart,
				"imag": imagPart,
			})
		}

		// Only add element if we calculated impedances (skip qy parameter)
		if len(impedances) > 0 {
			displayName := elementName
			if elementName == "qn" {
				displayName = "Q" // Show CPE as Q instead of qn
			}
			result = append(result, ElementImpedance{
				Name:       displayName,
				Impedances: impedances,
			})
		}
	}

	return result
}

func sendWebhook(requestID string, chiSquare float64, realImp []float64, imagImp []float64, frequencies []float64, parameters []float64, elementNames []string, elementImpedances []ElementImpedance, circuitType string) {
	// Handle NaN, Inf and other invalid float64 values for JSON marshaling
	validChiSquare := chiSquare
	if math.IsNaN(chiSquare) || math.IsInf(chiSquare, 0) {
		validChiSquare = 0.0 // Set to 0 instead of NaN for JSON compatibility
		log.Printf("Warning: Chi-square is invalid (%v), setting to 0.0 for JSON", chiSquare)
	}

	webhookData := WebhookResponse{
		ID:                 requestID,
		Time:               time.Now().Format(time.RFC3339Nano),
		ChiSquare:          validChiSquare,
		RealImpedance:      realImp,
		ImaginaryImpedance: imagImp,
		Frequencies:        frequencies,
		Parameters:         parameters,
		ElementNames:       elementNames,
		ElementImpedances:  elementImpedances,
		CircuitType:        circuitType,
	}

	jsonData, err := json.Marshal(webhookData)
	if err != nil {
		log.Printf("Error marshaling webhook data: %v", err)
		return
	}

	// Debug: Log a sample of the JSON payload to verify CircuitType is included
	if !globalConfig.Quiet {
		log.Printf("DEBUG: Webhook JSON sample - CircuitType: %s, ElementNames: %v", 
			webhookData.CircuitType, webhookData.ElementNames)
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error sending webhook: %v", err)
		return
	}
	defer resp.Body.Close()

	if !globalConfig.Quiet {
		log.Printf("Webhook sent - ID: %s, Chi-square: %.14e, CircuitType: %s, Status: %d", requestID, chiSquare, circuitType, resp.StatusCode)
	}
}
