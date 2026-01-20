package metrics

import "github.com/pkg/errors"

var (
	counterAlreadyExistsErr   = errors.New("counter already exists")
	gaugeAlreadyExistsErr     = errors.New("gauge already exists")
	histogramAlreadyExistsErr = errors.New("histogram already exists")
)
