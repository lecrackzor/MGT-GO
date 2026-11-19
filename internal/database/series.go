package database

import (
	"math"
	"strconv"
)

// ChartSeries is one column of samples. Missing values are NaN and encode as JSON null.
type ChartSeries []float64

// MarshalJSON writes NaN and Inf as null so a chart can leave a gap there.
func (s ChartSeries) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	buf := make([]byte, 0, len(s)*8+2)
	buf = append(buf, '[')
	for i, v := range s {
		if i > 0 {
			buf = append(buf, ',')
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			buf = append(buf, "null"...)
			continue
		}
		buf = strconv.AppendFloat(buf, v, 'g', -1, 64)
	}
	buf = append(buf, ']')
	return buf, nil
}
