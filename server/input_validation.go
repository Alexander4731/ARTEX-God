package server

import (
	"errors"
	"github.com/Autumn-27/artex/db"
	"net/http"
)

func writeConfigError(w http.ResponseWriter, err error) {
	var input *db.InputError
	if errors.As(err, &input) {
		writeJSON(w, 400, map[string]any{"error": input.Error(), "code": "invalid_enum", "field": input.Field})
		return
	}
	writeErr(w, 500, err.Error())
}
