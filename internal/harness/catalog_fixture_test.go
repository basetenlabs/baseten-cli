package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

type FixtureCatalog struct{ Path string }

func (f FixtureCatalog) Routes(ctx context.Context) ([]Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	d := json.NewDecoder(io.LimitReader(file, 1<<20))
	d.DisallowUnknownFields()
	var routes []Route
	if err := d.Decode(&routes); err != nil {
		return nil, errors.New("invalid internal catalog fixture")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("catalog fixture has trailing data")
	}
	return ValidateCatalog(routes)
}
