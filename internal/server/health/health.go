package health

import (
	"fmt"
	"net/http"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/health", health)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}
