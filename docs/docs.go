// Package docs Code generated by swaggo/swag. DO NOT EDIT
package docs

import "github.com/swaggo/swag"

const docTemplate = `{
    "schemes": {{ marshal .Schemes }},
    "swagger": "2.0",
    "info": {
        "description": "{{escape .Description}}",
        "title": "{{.Title}}",
        "contact": {},
        "version": "{{.Version}}"
    },
    "host": "{{.Host}}",
    "basePath": "{{.BasePath}}",
    "paths": {
        "/api/bindings": {
            "post": {
                "description": "Bind a queue to an exchange with the specified routing key",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "bindings"
                ],
                "summary": "Bind a queue to an exchange",
                "parameters": [
                    {
                        "description": "Binding details",
                        "name": "binding",
                        "in": "body",
                        "required": true,
                        "schema": {
                            "$ref": "#/definitions/models.BindQueueRequest"
                        }
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            },
            "delete": {
                "description": "Delete a binding from an exchange to a queue",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "bindings"
                ],
                "summary": "Delete a binding",
                "parameters": [
                    {
                        "description": "Binding to delete",
                        "name": "binding",
                        "in": "body",
                        "required": true,
                        "schema": {
                            "$ref": "#/definitions/models.DeleteBindingRequest"
                        }
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/bindings/{exchange}": {
            "get": {
                "description": "Get a list of all bindings for the specified exchange",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "bindings"
                ],
                "summary": "List all bindings for an exchange",
                "parameters": [
                    {
                        "type": "string",
                        "description": "Exchange name",
                        "name": "exchange",
                        "in": "path",
                        "required": true
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/exchanges": {
            "get": {
                "description": "Get a list of all exchanges",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "exchanges"
                ],
                "summary": "List all exchanges",
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            },
            "post": {
                "description": "Create a new exchange with the specified name and type",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "exchanges"
                ],
                "summary": "Create a new exchange",
                "parameters": [
                    {
                        "description": "Exchange to create",
                        "name": "exchange",
                        "in": "body",
                        "required": true,
                        "schema": {
                            "$ref": "#/definitions/models.CreateExchangeRequest"
                        }
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/exchanges/{exchange}": {
            "delete": {
                "description": "Delete an exchange with the specified name",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "exchanges"
                ],
                "summary": "Delete an exchange",
                "parameters": [
                    {
                        "type": "string",
                        "description": "Exchange name",
                        "name": "exchange",
                        "in": "path",
                        "required": true
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/queues": {
            "get": {
                "description": "Get a list of all queues",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "queues"
                ],
                "summary": "List all queues",
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            },
            "post": {
                "description": "Create a new queue with the specified name",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "queues"
                ],
                "summary": "Create a new queue",
                "parameters": [
                    {
                        "description": "Queue to create",
                        "name": "queue",
                        "in": "body",
                        "required": true,
                        "schema": {
                            "$ref": "#/definitions/models.CreateQueueRequest"
                        }
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/queues/{queue}": {
            "delete": {
                "description": "Delete a queue with the specified name",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "queues"
                ],
                "summary": "Delete a queue",
                "parameters": [
                    {
                        "type": "string",
                        "description": "Queue name",
                        "name": "queue",
                        "in": "path",
                        "required": true
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/queues/{queue}/consume": {
            "post": {
                "description": "Consume a message from the specified queue",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "queues"
                ],
                "summary": "Consume a message from a queue",
                "parameters": [
                    {
                        "type": "string",
                        "description": "Queue name",
                        "name": "queue",
                        "in": "path",
                        "required": true
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        },
        "/api/queues/{queue}/count": {
            "get": {
                "description": "Count the number of messages in the specified queue",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "queues"
                ],
                "summary": "Count messages in a queue",
                "parameters": [
                    {
                        "type": "string",
                        "description": "Queue name",
                        "name": "queue",
                        "in": "path",
                        "required": true
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "400": {
                        "description": "Bad Request",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    },
                    "500": {
                        "description": "Internal Server Error",
                        "schema": {
                            "$ref": "#/definitions/fiber.Map"
                        }
                    }
                }
            }
        }
    },
    "definitions": {
        "fiber.Map": {
            "type": "object",
            "additionalProperties": true
        },
        "models.BindQueueRequest": {
            "type": "object",
            "properties": {
                "exchange_name": {
                    "type": "string"
                },
                "queue_name": {
                    "type": "string"
                },
                "routing_key": {
                    "type": "string"
                }
            }
        },
        "models.CreateExchangeRequest": {
            "type": "object",
            "properties": {
                "exchange_name": {
                    "type": "string"
                },
                "exchange_type": {
                    "type": "string"
                }
            }
        },
        "models.CreateQueueRequest": {
            "type": "object",
            "properties": {
                "queue_name": {
                    "type": "string"
                }
            }
        },
        "models.DeleteBindingRequest": {
            "type": "object",
            "properties": {
                "exchange_name": {
                    "type": "string"
                },
                "queue_name": {
                    "type": "string"
                },
                "routing_key": {
                    "type": "string"
                }
            }
        }
    }
}`

// SwaggerInfo holds exported Swagger Info so clients can modify it
var SwaggerInfo = &swag.Spec{
	Version:          "",
	Host:             "",
	BasePath:         "",
	Schemes:          []string{},
	Title:            "",
	Description:      "",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  docTemplate,
	LeftDelim:        "{{",
	RightDelim:       "}}",
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
