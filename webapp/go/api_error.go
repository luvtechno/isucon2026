package isureal

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
)

type APIErrorResponse struct {
	Error string `json:"error"`
}

func APIErrorHandler(c *echo.Context, err error) {
	if response, _ := echo.UnwrapResponse(c.Response()); response != nil && response.Committed {
		return
	}
	status, message := http.StatusInternalServerError, err.Error()
	var apiErr *APIError
	var echoErr echo.HTTPStatusCoder
	switch {
	case errors.As(err, &apiErr):
		status, message = apiErr.Status, apiErr.Code
	case errors.As(err, &echoErr):
		status = echoErr.StatusCode()
	}
	if status >= http.StatusInternalServerError {
		c.Logger().Error("request failed", "error", err)
	}
	body := APIErrorResponse{Error: message}
	if writeErr := c.JSON(status, body); writeErr != nil {
		c.Logger().Error("failed to write error response", "error", writeErr)
	}
}
