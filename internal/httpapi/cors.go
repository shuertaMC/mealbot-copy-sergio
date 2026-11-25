package httpapi

import "net/http"

const (
	AllowedHeaders = "Authorization, Content-Type, Origin, Accept, token"
	AllowedMethods = "GET, POST, DELETE"
)

// CORSConfig captures the intentionally permissive compatibility behavior.
// Origin allowlisting is a separate product decision and is not implemented.
type CORSConfig struct {
	ReflectOrigin  bool
	AllowedHeaders string
	AllowedMethods string
}

// CompatibilityCORS returns the exact characterized CORS configuration.
func CompatibilityCORS() CORSConfig {
	return CORSConfig{
		ReflectOrigin:  true,
		AllowedHeaders: AllowedHeaders,
		AllowedMethods: AllowedMethods,
	}
}

func (c CORSConfig) normalized() CORSConfig {
	// Compatibility always reflects Origin. An allowlist or disabled CORS mode
	// would be a separate product decision rather than a transport default.
	c.ReflectOrigin = true
	if c.AllowedHeaders == "" {
		c.AllowedHeaders = AllowedHeaders
	}
	if c.AllowedMethods == "" {
		c.AllowedMethods = AllowedMethods
	}
	return c
}

// CORSMiddleware reflects the request origin and terminates OPTIONS before the
// authentication middleware or route handler can run.
func CORSMiddleware(config CORSConfig) func(http.Handler) http.Handler {
	config = config.normalized()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config.ReflectOrigin {
				w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			}
			w.Header().Set("Access-Control-Allow-Headers", config.AllowedHeaders)
			w.Header().Set("Access-Control-Allow-Methods", config.AllowedMethods)

			if r.Method == http.MethodOptions {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS applies the exact legacy policy.
func CORS(next http.Handler) http.Handler {
	return CORSMiddleware(CompatibilityCORS())(next)
}
