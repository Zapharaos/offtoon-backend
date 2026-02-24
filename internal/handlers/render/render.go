package render

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"

	"go.uber.org/zap"
)

type ErrorResponse struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

type CountResponse struct {
	Count int64 `json:"count"`
}

type BooleanResponse struct {
	Value bool `json:"value"`
}

// Error returns an HTTP status 500 with a specific error message
func Error(w http.ResponseWriter, r *http.Request, err error, message string) {
	resp := ErrorResponse{Title: "Internal Server Error - Please contact the administrator."}
	if err != nil {
		if message != "" {
			zap.L().Error(message, zap.Error(err))
		} else {
			resp.Message = err.Error()
		}
	}
	w.WriteHeader(http.StatusInternalServerError)
	JSON(w, r, resp)
}

// BadRequest returns an HTTP status 400 with a specific error message
func BadRequest(w http.ResponseWriter, r *http.Request, err error) {
	resp := ErrorResponse{Title: "Bad Request - Please check your request."}
	if err != nil {
		zap.L().Debug("Bad Request", zap.Error(err))
		resp.Message = err.Error()
	}
	w.WriteHeader(http.StatusBadRequest)
	JSON(w, r, resp)
}

// OK returns an HTTP status 200 with an empty body
func OK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// NotImplemented returns an HTTP status 501
func NotImplemented(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	JSON(w, r, map[string]interface{}{"message": "Not Implemented"})
}

// JSON try to encode an interface and returns it in a specific ResponseWriter (or returns an internal server error)
func JSON(w http.ResponseWriter, r *http.Request, data interface{}) {
	OK(w, r)

	err := json.NewEncoder(w).Encode(data)
	if err != nil {
		zap.L().Error("Render JSON encode", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

// NotFound returns an HTTP status 404 with a specific error message
func NotFound(w http.ResponseWriter, r *http.Request, err error) {
	resp := ErrorResponse{Title: "Not Found"}
	if err != nil {
		zap.L().Debug("Not Found", zap.Error(err))
		resp.Message = err.Error()
	}
	w.WriteHeader(http.StatusNotFound)
	JSON(w, r, resp)
}

func Accepted(w http.ResponseWriter, r *http.Request, data interface{}) {
	w.WriteHeader(http.StatusAccepted)
	JSON(w, r, data)
}

// Count returns an HTTP status 200 with a JSON object containing the count (CountResponse)
func Count(w http.ResponseWriter, r *http.Request, count int64) {
	JSON(w, r, CountResponse{Count: count})
}

// StreamFile handle files streamed response with allows the download of a file in chunks
func StreamFile(filePath, fileName string, w http.ResponseWriter, r *http.Request) {
	file, err := os.Open(filePath)
	if err != nil {
		Error(w, r, err, "Failed to open file")
		return
	}
	defer file.Close()

	// Set all necessary headers
	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(fileName))
	w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
	w.Header().Set("Content-Type", "application/octet-stream")

	const bufferSize = 4096
	buffer := make([]byte, bufferSize)

	for {
		// Read a chunk of the file
		bytesRead, err := file.Read(buffer)
		if err == io.EOF {
			break
		} else if err != nil {
			Error(w, r, err, "Failed to read file")
			return
		}

		// Write the chunk to the response writer
		_, err = w.Write(buffer[:bytesRead])
		if err != nil {
			// If writing to the response writer fails, log the error and stop streaming
			Error(w, r, err, "Failed to write to response")
			break
		}

		w.(http.Flusher).Flush()
	}
}
