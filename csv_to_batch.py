#!/usr/bin/env python3

import csv
import json
import sys
from datetime import datetime
from collections import defaultdict

def convert_csv_to_batch(csv_file, batch_id="csv-batch-001"):
    """
    Convert combined_impedance_data.csv to batch JSON format
    
    CSV format: Frequency_Hz,Z_real,Z_imag,Spectrum_Number
    """
    
    # Read and group data by spectrum number
    spectra_data = defaultdict(list)
    
    with open(csv_file, 'r') as f:
        reader = csv.DictReader(f)
        for row in reader:
            spectrum_num = int(row['Spectrum_Number'])
            frequency = float(row['Frequency_Hz'])
            z_real = float(row['Z_real'])
            z_imag = float(row['Z_imag'])
            
            spectra_data[spectrum_num].append({
                'frequency': frequency,
                'real': z_real,
                'imag': z_imag
            })
    
    # Sort each spectrum by frequency (ascending)
    for spectrum_num in spectra_data:
        spectra_data[spectrum_num].sort(key=lambda x: x['frequency'])
    
    # Create batch JSON structure
    batch = {
        "batch_id": batch_id,
        "timestamp": datetime.now().isoformat() + "Z",
        "spectra": []
    }
    
    # Convert each spectrum to the required format
    for spectrum_num in sorted(spectra_data.keys()):
        data_points = spectra_data[spectrum_num]
        
        # Extract frequencies and impedance data
        frequencies = [point['frequency'] for point in data_points]
        impedances = [{"real": point['real'], "imag": point['imag']} for point in data_points]
        
        spectrum_entry = {
            "iteration": spectrum_num - 1,  # Convert to 0-based indexing
            "impedance_data": {
                "timestamp": datetime.now().isoformat() + "Z",
                "frequencies": frequencies,
                "magnitude": [],  # Not provided in CSV, but part of schema
                "phase": [],     # Not provided in CSV, but part of schema  
                "impedance": impedances
            }
        }
        
        batch["spectra"].append(spectrum_entry)
    
    return batch

def main():
    if len(sys.argv) < 2:
        print("Usage: python3 csv_to_batch.py <csv_file> [batch_id]")
        sys.exit(1)
    
    csv_file = sys.argv[1]
    batch_id = sys.argv[2] if len(sys.argv) > 2 else "csv-batch-001"
    
    try:
        batch_data = convert_csv_to_batch(csv_file, batch_id)
        
        # Print statistics
        print(f"Converted CSV to batch JSON:", file=sys.stderr)
        print(f"  Batch ID: {batch_data['batch_id']}", file=sys.stderr)
        print(f"  Spectra: {len(batch_data['spectra'])}", file=sys.stderr)
        if batch_data['spectra']:
            print(f"  Frequencies per spectrum: {len(batch_data['spectra'][0]['impedance_data']['frequencies'])}", file=sys.stderr)
        print(f"  Total data points: {sum(len(s['impedance_data']['frequencies']) for s in batch_data['spectra'])}", file=sys.stderr)
        print("", file=sys.stderr)
        
        # Output JSON to stdout
        json.dump(batch_data, sys.stdout, indent=2)
        
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()