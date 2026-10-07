package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"
)

type spyReadCloser struct {
	io.ReadCloser
	bytesRead int
}

func (r *spyReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytesRead += n
	return n, err
}

type spyResponseWriter struct {
	http.ResponseWriter
	bytesWritten int
	statusCode   int
}

func (w *spyResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += n
	return n, err
}

func (w *spyResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

const logContextKey contextKey = "log_context"

type LogContext struct {
	Username string
	Error    error
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			spyReader := &spyReadCloser{ReadCloser: r.Body}
			r.Body = spyReader
			spyWriter := &spyResponseWriter{ResponseWriter: w}
			//context.WithValue(logContext, "Username", logContextKey)
			logContext := &LogContext{}
			r = r.WithContext(context.WithValue(r.Context(), logContextKey, logContext))
			next.ServeHTTP(spyWriter, r)
			attrs := []any{
				slog.String("request_id", spyWriter.Header().Get("X-Request-ID")),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("client_ip", redactIP(r.RemoteAddr)),
				slog.Duration("duration", time.Since(start)),
				slog.Int("request_body_bytes", spyReader.bytesRead),
				slog.Int("response_status", spyWriter.statusCode),
				slog.Int("response_body_bytes", spyWriter.bytesWritten),
			}
			if logContext.Username != "" {
				attrs = append(attrs, slog.String("user", logContext.Username))
			}
			if logContext.Error != nil {
				attrs = append(attrs, slog.Any("error", logContext.Error))
			}
			logger.Info("Served request", attrs...)
		})
	}
}

func httpError(ctx context.Context, w http.ResponseWriter, status int, err error) {
	if logCtx, ok := ctx.Value(logContextKey).(*LogContext); ok {
		logCtx.Error = err
	}
	badStatus := []int{401, 403, 500}
	errString := err.Error()
	if slices.Contains(badStatus, status) {
		errString = http.StatusText(status)
	}
	http.Error(w, errString, status)
}

func redactIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if ip4 := ip.To4(); ip4 != nil {
		return fmt.Sprintf("%d.%d.%d.x", ip4[0], ip4[1], ip4[2])
	}
	return ip.String()
}
