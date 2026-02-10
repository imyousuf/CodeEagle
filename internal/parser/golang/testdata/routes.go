package routes

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
)

// Gin route handlers.

func listUsers(c *gin.Context)   {}
func getUser(c *gin.Context)     {}
func createUser(c *gin.Context)  {}
func updateUser(c *gin.Context)  {}
func deleteUser(c *gin.Context)  {}
func healthCheck(c *gin.Context) {}
func listItems(c *gin.Context)   {}
func createItem(c *gin.Context)  {}

// SetupGinRoutes registers Gin routes.
func SetupGinRoutes(r *gin.Engine) {
	r.GET("/health", healthCheck)
	r.POST("/users", createUser)
	r.PUT("/users/:id", updateUser)
	r.DELETE("/users/:id", deleteUser)
}

// SetupGinGroupRoutes registers Gin routes with router groups.
func SetupGinGroupRoutes(router *gin.Engine) {
	api := router.Group("/api/v1")
	api.GET("/users", listUsers)
	api.POST("/users", createUser)

	items := api.Group("/items")
	items.GET("/", listItems)
	items.POST("/", createItem)
}

// Net/http route handlers.

func netHTTPHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "hello")
}

func aboutHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "about")
}

// SetupNetHTTPRoutes registers net/http routes.
func SetupNetHTTPRoutes() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/data", netHTTPHandler)
	mux.Handle("/api/about", http.HandlerFunc(aboutHandler))
	http.HandleFunc("/status", netHTTPHandler)
}

// Gorilla mux route handlers.

func gorillGetUsers(w http.ResponseWriter, r *http.Request)    {}
func gorillaGetUser(w http.ResponseWriter, r *http.Request)    {}
func gorillaCreateUser(w http.ResponseWriter, r *http.Request) {}

// SetupGorillaMuxRoutes registers gorilla/mux routes.
func SetupGorillaMuxRoutes() {
	r := mux.NewRouter()
	r.HandleFunc("/api/users", gorillGetUsers).Methods("GET")
	r.HandleFunc("/api/users/{id}", gorillaGetUser).Methods("GET")
	r.HandleFunc("/api/users", gorillaCreateUser).Methods("POST")
}

// Ensure imports are used (avoids compile errors in test fixtures).
var _ = gin.New
var _ = mux.NewRouter
