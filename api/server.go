package api

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"

	"github.com/simpleiot/simpleiot/db"
)

// IndexHandler is used to serve the index page
type IndexHandler struct {
	getAsset func(string) []byte
}

func (h *IndexHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	fmt.Println("indexHandler")
	f := h.getAsset("/index.html")
	if f == nil {
		rw.WriteHeader(http.StatusNotFound)
	} else {
		var reader = bytes.NewBuffer(f)
		io.Copy(rw, reader)
	}
}

// NewIndexHandler returns a new Index handler
func NewIndexHandler(getAsset func(string) []byte) http.Handler {
	return &IndexHandler{getAsset: getAsset}
}

// App is a struct that implements http.Handler interface
type App struct {
	PublicHandler http.Handler
	IndexHandler  http.Handler
	V1ApiHandler  http.Handler
	Debug         bool
}

// Top level handler for http requests in the coap-server process
func (h *App) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	var head string

	if h.Debug {
		dump, _ := httputil.DumpRequest(req, true)
		body, err := req.GetBody()
		if err == nil {
			buf := bytes.Buffer{}
			_, err = buf.ReadFrom(body)
			log.Printf("HTTP %v: %v -> %v\n", req.Method, req.URL.Path, string(buf.Bytes()))
		} else {
			log.Printf("HTTP %v: %v\n", req.Method, req.URL.Path)
		}
	}

	switch req.URL.Path {
	case "/", "/orgs", "/users", "/devices", "/sign-in":
		h.IndexHandler.ServeHTTP(res, req)

	default:
		head, req.URL.Path = ShiftPath(req.URL.Path)
		switch head {
		case "public":
			h.PublicHandler.ServeHTTP(res, req)
		case "v1":
			h.V1ApiHandler.ServeHTTP(res, req)
		default:
			http.Error(res, "Not Found", http.StatusNotFound)
		}
	}
}

// NewAppHandler returns a new application (root) http handler
func NewAppHandler(args ServerArgs) http.Handler {
	return &App{
		PublicHandler: http.FileServer(args.Filesystem),
		IndexHandler:  NewIndexHandler(args.GetAsset),
		V1ApiHandler:  NewV1Handler(args.DbInst, args.Influx, args.Auth),
		Debug:         args.Debug,
	}
}

// ServerArgs can be used to pass arguments to the server subsystem
type ServerArgs struct {
	Port       string
	DbInst     *Db
	Influx     *db.Influx
	GetAsset   func(string) []byte
	Filesystem http.FileSystem
	Debug      bool
	Auth       Authorizer
}

// Server starts a API server instance
func Server(args ServerArgs) error {
	log.Println("Starting http server, debug: ", args.Debug)
	log.Println("Starting portal on port: ", args.Port)
	address := fmt.Sprintf(":%s", args.Port)
	return http.ListenAndServe(address, NewAppHandler(args))
}
