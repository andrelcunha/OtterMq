package web

import (
	"os"
	"time"

	"github.com/andrelcunha/ottermq/internal/core/broker"
	// "github.com/andrelcunha/ottermq/pkg/connection/client"
	"github.com/andrelcunha/ottermq/web/handlers/api"
	"github.com/andrelcunha/ottermq/web/handlers/api_admin"
	"github.com/andrelcunha/ottermq/web/handlers/webui"
	"github.com/andrelcunha/ottermq/web/middleware"

	"github.com/gofiber/contrib/swagger"
	"github.com/gofiber/fiber/v2"
	"github.com/rabbitmq/amqp091-go"
)

type WebServer struct {
	brokerAddr string
	// conn              net.Conn
	heartbeatInterval time.Duration
	config            *Config
	Broker            *broker.Broker
	// Client            *client.Client
	Client *amqp091.Connection
}

type Config struct {
	BrokerHost string
	BrokerPort string
	// HeartbeatInterval int
	Username string
	Password string
	JwtKey   string
}

func (ws *WebServer) Close() {
	// ws.conn.Close()
}

func NewWebServer(config *Config, broker *broker.Broker, conn *amqp091.Connection) (*WebServer, error) {
	// connectionSting := fmt.Sprintf("amqp://%s:%s@%s:%s/", config.Username, config.Password, config.BrokerHost, config.BrokerPort)
	// conn, err := getBrokerClient(connectionSting)
	// if err != nil {
	// 	return nil, err
	// }

	return &WebServer{
		// brokerAddr: brokerAddr,
		// conn:              conn,
		// heartbeatInterval: time.Duration(config.HeartbeatInterval) * time.Second,
		config: config,
		Broker: broker,
		Client: conn,
	}, nil
}

func (ws *WebServer) SetupApp(logFile *os.File) *fiber.App {
	// connectionSting := fmt.Sprintf("amqp://%s:%s@%s:%s/", ws.config.Username, ws.config.Password, ws.config.BrokerHost, ws.config.BrokerPort)
	// conn, err := getBrokerClient(connectionSting)

	// ws.Client = conn
	app := ws.configServer(logFile)

	ws.AddSwagger(app)

	// Serve static files
	app.Static("/", "./web/static")

	app.Get("/login", webui.LoginPage)
	app.Post("/login", webui.Authenticate)

	ws.AddApi(app)

	ws.AddAdminApi(app)

	ws.AddUI(app)

	return app
}

func (ws *WebServer) AddApi(app *fiber.App) {
	// API routes
	apiGrp := app.Group("/api/")
	apiGrp.Get("/queues", api.ListQueues)
	apiGrp.Post("/queues", api.CreateQueue)
	apiGrp.Delete("/queues/:queue", api.DeleteQueue)
	apiGrp.Post("/queues/:queue/consume", api.ConsumeMessage)
	apiGrp.Get("/queues/:queue/count", api.CountMessages)
	apiGrp.Post("/messages/:id/ack", api.AckMessage)
	apiGrp.Post("/messages", api.PublishMessage)
	apiGrp.Get("/exchanges", api.ListExchanges)
	apiGrp.Post("/exchanges", api.CreateExchange)
	apiGrp.Delete("/exchanges/:exchange", api.DeleteExchange)
	apiGrp.Get("/bindings/:exchange", api.ListBindings)
	apiGrp.Post("/bindings", api.BindQueue)
	apiGrp.Delete("/bindings", api.DeleteBinding)
	apiGrp.Get("/connections", func(c *fiber.Ctx) error {
		return api.ListConnections(c, ws.Broker)
	})
	apiGrp.Post("/login", api_admin.Login)
}

func (ws *WebServer) AddSwagger(app *fiber.App) {
	swaggerCfg := swagger.Config{
		BasePath: "/api/",
		FilePath: "./web/static/docs/swagger.json",
		Path:     "docs",
		Title:    "OtterMQ API",
	}
	swaggerHandler := swagger.New(swaggerCfg)
	app.Use(swaggerHandler)
}

func (ws *WebServer) AddUI(app *fiber.App) {
	// Web Interface Routes
	webGrp := app.Group("/", middleware.AuthRequired)
	webGrp.Get("/", webui.Dashboard)
	webGrp.Get("/logout", webui.Logout)
	webGrp.Get("/overview", webui.Dashboard)
	webGrp.Get("/connections", webui.ListConnections)
	webGrp.Get("/exchanges", webui.ListExchanges)
	webGrp.Get("/queues", webui.ListQueues)
	// webGrp.Get("/settings", webui.Settings)
}

func (ws *WebServer) AddAdminApi(app *fiber.App) {
	// Admin API routes
	apiAdminGrp := app.Group("/api/admin")
	apiAdminGrp.Use(middleware.JwtMiddleware(ws.config.JwtKey))
	apiAdminGrp.Get("/users", api_admin.GetUsers)
	apiAdminGrp.Post("/users", api_admin.AddUser)
}
