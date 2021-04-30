package webserver

import (
	"fmt"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"io"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Server struct {
	endpoints map[string]http.Handler
	logger    *logrus.Logger
}

var LoggingMiddleware mux.MiddlewareFunc = func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s - %s\n", r.Method, r.RequestURI)
		next.ServeHTTP(w, r)
	})
}

func (s Server) Start(listenAddr string) error {
	r := mux.NewRouter()
	r.Use(LoggingMiddleware)
	for _, e := range s.endpoints {
		switch (e).(type) {
		case NotFoundFileHandler:
			r.NotFoundHandler = LoggingMiddleware(e)
		default:
		}
	}

	for k, e := range s.endpoints {
		switch (e).(type) {
		case filesystemHandler:
			fsHandler := e.(filesystemHandler)
			fsHandler.notFoundHandler = r.NotFoundHandler
			s.endpoints[k] = fsHandler
			fmt.Printf("%s = %v\n", k, fsHandler)
			r.NewRoute().PathPrefix(k).Handler(fsHandler)
		case NotFoundFileHandler:
		default:
			fmt.Printf("%s = %v\n", k, e)
			r.NewRoute().PathPrefix(k).Handler(e)
		}
	}

	return http.ListenAndServe(listenAddr, r)
}

func reverseProxy(dest string) (*httputil.ReverseProxy, error) {
	destUrl, err := url.Parse(dest)
	if err != nil {
		return nil, fmt.Errorf("unable to parse URL: %v", err)
	}

	director := func(req *http.Request) {
		req.URL.Scheme = destUrl.Scheme
		req.URL.Host = destUrl.Host
		req.URL.Path = destUrl.Path + req.URL.Path
		req.Host = destUrl.Host
	}
	proxy := &httputil.ReverseProxy{Director: director}
	return proxy, nil
}

func New(endpoints []string) (*Server, error) {
	server := Server{endpoints: map[string]http.Handler{}}
	server.logger = logrus.New()
	for _, e := range endpoints {
		parsedEndpoint, err := server.parseEndpoint(e)
		if err != nil {
			return nil, fmt.Errorf("unable to parse endpoint: %v", err)
		}
		server.endpoints[parsedEndpoint.MountPoint] = parsedEndpoint.Destination
	}
	return &server, nil
}

type Endpoint struct {
	MountPoint  string
	Destination http.Handler
}

func (s *Server) parseEndpoint(e string) (*Endpoint, error) {
	split := strings.SplitAfterN(e, ":", 2)
	source := strings.Replace(split[0], ":", "", 1)
	destObj, err := s.parseDest(split[1])
	if err != nil {
		return nil, err
	}
	return &Endpoint{MountPoint: source, Destination: destObj}, nil
}

func (s *Server) parseDest(dest string) (http.Handler, error) {
	split := strings.SplitAfterN(dest, "=", 2)
	typeName := strings.Replace(split[0], "=", "", 1)
	argument := split[1]

	switch typeName {
	case "proxy":
		dest, err := reverseProxy(argument)
		if err != nil {
			return nil, fmt.Errorf("unable to parse proxy: %v", err)
		}
		return dest, nil
	case "file":
		return s.staticFiles(argument), nil
	case "404":
		return notFound(argument), nil
	}

	return nil, fmt.Errorf("unknown type %s", typeName)
}

type NotFoundFileHandler struct {
	filePath string
}

func (n NotFoundFileHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f, err := os.Open(n.filePath)
	if err != nil {
		_, _ = writer.Write([]byte("unable to get 404 page"))
		return
	}

	_, err = io.Copy(writer, f)
	if err != nil {
		_, _ = writer.Write([]byte("unable to send 404 page"))
		return
	}
}

func NewNotFoundHandler(filePath string) NotFoundFileHandler {
	return NotFoundFileHandler{filePath: filePath}
}

func notFound(file string) http.Handler {
	return NewNotFoundHandler(file)
}

type filesystemHandler struct {
	path            string
	notFoundHandler http.Handler
	logger          *logrus.Logger
}

func (s *Server) newFSHandler(path string) filesystemHandler {
	return filesystemHandler{
		path: path,
		logger: s.logger,
	}
}

func (f filesystemHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	uri := request.RequestURI
	if strings.HasPrefix(uri, "/") {
		uri = uri[1:]
	}

	if uri == "" {
		uri = "./"
	}

	fullPath := filepath.Join(f.path, "/", uri)
	cleanPath := path.Clean(fullPath)
	fromSlash := filepath.FromSlash(cleanPath)

	if !strings.HasPrefix(fromSlash, filepath.FromSlash(f.path)) {
		f.logger.Printf("path traversal: %v", fromSlash)
		http.Error(writer, "404 - Not found", http.StatusNotFound)
		return
	}

	stat, err := os.Stat(fromSlash)
	if err != nil {
		f.logger.Printf("unable to open file %s: %s", uri, err.Error())
		if f.notFoundHandler == nil {
			http.Error(writer, "404 - Not found", http.StatusNotFound)
			return
		}
		f.notFoundHandler.ServeHTTP(writer, request)
		return
	}

	if stat.IsDir() {
		fromSlash += "/index.html"
	}

	file, err := os.Open(fromSlash)
	if err != nil {
		f.logger.Printf("unable to open file %s: %s", uri, err.Error())
		if f.notFoundHandler == nil {
			http.Error(writer, "404 - Not found", http.StatusNotFound)
			return
		}
		f.notFoundHandler.ServeHTTP(writer, request)
		return
	}
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension("." + filepath.Ext(fromSlash))
	writer.Header().Set("Content-Type", contentType)
	_, _ = writer.Write(fileBytes)
}

var _ http.Handler = filesystemHandler{}

func (s *Server) staticFiles(path string) http.Handler {
	return s.newFSHandler(path)
}
