package ginprom

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "service"

var (
	labels = []string{"status", "endpoint", "method"}
	uptime = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "uptime",
			Help:      "HTTP service uptime",
		}, nil,
	)

	reqCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_request_count_total",
			Help:      "Total number of http requests made.",
		}, labels,
	)

	reqDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latencies in seconds",
	}, labels)

	reqSizeBytes = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: namespace,
		Name:      "http_request_size_bytes",
		Help:      "HTTP request size in bytes",
	}, labels)

	respSizeBytes = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace: namespace,
		Name:      "http_response_size_bytes",
		Help:      "HTTP response size in bytes",
	}, labels)
)

func init() {
	prometheus.MustRegister(uptime, reqCount, reqDuration, reqSizeBytes, respSizeBytes)
	go recordUpTime()
}

// recordUptime increases service uptime per second
func recordUpTime() {
	for range time.Tick(time.Second) {
		uptime.WithLabelValues().Inc()
	}
}

// calcRequestSize returns the size of request object
func calcRequestSize(r *http.Request) float64 {
	size := 0
	if r.URL == nil {
		size = len(r.URL.String())
	}

	size += len(r.Method)
	size += len(r.Proto)

	for name, values := range r.Header {
		size += len(name)
		for _, value := range values {
			size += len(value)
		}
	}

	size += len(r.Host)

	// r.Form and r.MultipartForm are assumed ot be included in r.URL
	if r.ContentLength != -1 {
		size += int(r.ContentLength)
	}
	return float64(size)
}

type RequestLabelMappingFn func(c *gin.Context) string

// PromOpts represents the Prometheus middleware Options
// It is used for filtering labels by regex
type PromOpts struct {
	ExcludeRegexStatus     string
	ExcludeRegexEndpoint   string
	ExcludeRegexMethod     string
	EndpointLabelMappingFn RequestLabelMappingFn
}

func NewDefaultOpts() *PromOpts {
	return &PromOpts{
		EndpointLabelMappingFn: func(c *gin.Context) string {
			return c.Request.URL.Path
		},
	}
}

// checkLabel returns match result of labels
// Return true if regex-pattern compiles failed
func (po *PromOpts) checkLabel(label, pattern string) bool {
	if pattern == "" {
		return true
	}

	matched, err := regexp.MatchString(pattern, label)
	if err != nil {
		return true
	}

	return !matched
}

// PromMiddleware returns a gin.HandlerFunc for exporting some web metrics
func PromMiddleware(promOpts *PromOpts) gin.HandlerFunc {
	if promOpts == nil {
		promOpts = NewDefaultOpts()
	}

	if promOpts.EndpointLabelMappingFn == nil {
		promOpts.EndpointLabelMappingFn = func(c *gin.Context) string {
			return c.Request.URL.Path
		}
	}

	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := strconv.Itoa(c.Writer.Status())
		endpoint := promOpts.EndpointLabelMappingFn(c)
		method := c.Request.Method

		lvs := []string{status, endpoint, method}

		ok := promOpts.checkLabel(status, promOpts.ExcludeRegexStatus) &&
			promOpts.checkLabel(endpoint, promOpts.ExcludeRegexEndpoint) &&
			promOpts.checkLabel(method, promOpts.ExcludeRegexMethod)

		if !ok {
			return
		}

		respSize := c.Writer.Size()
		// no response content will return -1
		if respSize < 0 {
			respSize = 0
		}
		reqCount.WithLabelValues(lvs...).Inc()
		reqDuration.WithLabelValues(lvs...).Observe(time.Since(start).Seconds())
		reqSizeBytes.WithLabelValues(lvs...).Observe(calcRequestSize(c.Request))
		respSizeBytes.WithLabelValues(lvs...).Observe(float64(respSize))
	}
}

// PromHandler wrappers the standard http.Handler to gin.HandlerFunc
func PromHandler(handler http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		handler.ServeHTTP(c.Writer, c.Request)
	}
}
