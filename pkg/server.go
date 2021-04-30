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
	endpoints []*Endpoint
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
		switch (e.Destination).(type) {
		case NotFoundFileHandler:
			r.NotFoundHandler = LoggingMiddleware(e.Destination)
		default:
		}
	}

	for _, e := range s.endpoints {
		switch (e.Destination).(type) {
		case filesystemHandler:
			fsHandler := e.Destination.(filesystemHandler)
			fsHandler.notFoundHandler = r.NotFoundHandler
			fmt.Printf("%s = %v\n", e.MountPoint, fsHandler)
			r.NewRoute().PathPrefix(e.MountPoint).Handler(fsHandler)
		case NotFoundFileHandler:
		default:
			fmt.Printf("%s = %v\n", e.MountPoint, e.Destination)
			r.NewRoute().PathPrefix(e.MountPoint).Handler(e.Destination)
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
	}
	proxy := &httputil.ReverseProxy{Director: director}
	return proxy, nil
}

func New(endpoints []string) (*Server, error) {
	server := Server{endpoints: nil}
	server.logger = logrus.New()
	server.logger.SetLevel(logrus.DebugLevel)
	for _, e := range endpoints {
		parsedEndpoint, err := server.parseEndpoint(e)
		if err != nil {
			return nil, fmt.Errorf("unable to parse endpoint: %v", err)
		}
		server.endpoints = append(server.endpoints, parsedEndpoint)
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
	absPath, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	s.logger.Debugf("serving from %s", absPath)
	return filesystemHandler{
		path: absPath,
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
	absFilePath, err := filepath.Abs(cleanPath)
	if err != nil {
		f.logger.Printf("cannot get abs path for %s: %v", cleanPath, err)
		http.Error(writer, "404 - Not found", http.StatusNotFound)
		return
	}

	if !strings.HasPrefix(absFilePath, f.path) {
		f.logger.Printf("path traversal: %v", absFilePath)
		http.Error(writer, "404 - Not found", http.StatusNotFound)
		return
	}

	stat, err := os.Stat(absFilePath)
	if err != nil {
		f.logger.Printf("unable to open file %s: %s", uri, err.Error())
		if f.notFoundHandler == nil {
			http.Error(writer, "404 - Not found (not found handler missing)", http.StatusNotFound)
			return
		}
		f.notFoundHandler.ServeHTTP(writer, request)
		return
	}

	if stat.IsDir() {
		absFilePath += "/index.html"
	}

	file, err := os.Open(absFilePath)
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
	contentType := mime.TypeByExtension("." + filepath.Ext(absFilePath))
	writer.Header().Set("Content-Type", contentType)
	_, _ = writer.Write(fileBytes)
}

var _ http.Handler = filesystemHandler{}

func (s *Server) staticFiles(path string) http.Handler {
	return s.newFSHandler(path)
}
