package pms

import "errors"

// QueryFailure carries safe diagnostics, never the raw response or credentials.
type QueryFailure struct {
	Kind       string `json:"errorKind"`
	Code       string `json:"businessCode,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Message    string `json:"message"`
}

func (e *QueryFailure) Error() string { return e.Message }

func QueryErrorDetails(err error) QueryFailure {
	var failure *QueryFailure
	if errors.As(err, &failure) {
		return *failure
	}
	return QueryFailure{Kind: "unavailable", Message: err.Error()}
}

func queryBusinessFailure(code, message string) *QueryFailure {
	kind := "unavailable"
	switch code {
	case "401", "403":
		kind = "denied"
	case "1010005013", "1010005035", "563":
		kind = "empty"
	case "1010005016", "512":
		kind = "invalid_state"
	case "640":
		kind = "needs_input"
	}
	return &QueryFailure{Kind: kind, Code: code, HTTPStatus: 200, Message: message}
}
