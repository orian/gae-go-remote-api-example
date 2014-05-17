package backend

import (
	_ "appengine/remote_api"
	"backend/handlers"
	"net/http"
)

func init() {
	http.HandleFunc("/", handlers.Main)
	http.HandleFunc("/helloWorld", handlers.HelloWorld)
}
