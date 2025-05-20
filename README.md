# gkBoot

| Build Status  |
|---|
| [![Go](https://github.com/Yomiji/gkBoot/actions/workflows/go.yml/badge.svg?event=push)](https://github.com/Yomiji/gkBoot/actions/workflows/go.yml)  |

## Objective

The purpose of gkBoot is to organize, compartmentalize and wire
a microservice with the least amount of boilerplate while providing devs
an easier time in building a microservice.

The inspiration for this is mostly Spring Boot. I liked the look of the
decoupled architecture and the adherence to a domain-oriented design
pattern.

In gkBoot, I tried to capture much of that while still maintaining that
which makes Go great. Hopefully you like it too.

## Installation
*Note: Please use go 1.24 for installation*
```bash
go get github.com/yomiji/gkBoot@v1.6.0
```

## Use
*Note: Please check out tests for advanced or detailed use cases*

Users of gkBoot are recommended to follow a pattern when creating their
microservics. Generally speaking, everything is centered around the
service wiring (which sit in main.go) and the service files (which sit
in the service directory):
```text
├── go.mod
├── go.sum
├── main.go
├── services
│   └── greeting
│       └── greetings.go
└── tests
    └── greeting
        └── greetings_test.go
```
In services/greeting/greetings.go:
```go
package greeting
import (
    "context"
    "fmt"
    "strconv"
    "github.com/yomiji/gkBoot"
    "github.com/yomiji/gkBoot/request"
)

type Request struct {
	// the "header!" tag value indicates that the value is required to be in
	// the header with the alias indicating that it should be named Secret-Value
	SecretValue string `header:"Secret-Value" required:"true" json:"-"`
	// the "query" tag value indicates that the value of the object is found in
	// the request query params
	FirstName   string `query:"firstName" json:"firstName"`
	// the "path" tag value indicates that the value of the object is found in
	// the url request path
	Age         int    `path:"age" json:"age"`
}

func (r Request) Info() request.HttpRouteInfo {
	return request.HttpRouteInfo{
		Name:        "DemoRequest",
		Method:      request.GET,
		Path:        "/{age}/greetings",
		Description: "A typical greeting.",
	}
}

type Service struct {
	gkBoot.BasicService
}

type Response struct {
	Greeting string
}

func (s Service) Execute(ctx context.Context, request interface{}) (response interface{}, err error) {
	reqObj := request.(*Request)
	
	var age string
	
	if reqObj.Age == 0 {
		age = "old"
	} else {
		age = strconv.Itoa(reqObj.Age)
	}
	
	greeting := fmt.Sprintf("Hello, %s! You're %s!\n", reqObj.FirstName, age)
	
	return Response{Greeting:greeting}, nil
}

```

In main.go:
```go
package main
import (
	"greeting"
    "github.com/yomiji/gkBoot"
)

func main() {
    // start an http service on localhost port 8080
    gkBoot.StartServer([]gkBoot.ServiceRequest{
        {
            Request: new(greeting.Request),
            Service: new(greeting.Service),
        },
    })
}
```

In a client:
```go
package main
import (
	"greeting"
	"github.com/yomiji/gkBoot"
)

func main() {
    Request := &greeting.Request {
        SecretValue: "Hello!",
        FirstName: "Simon!",
        Age: 21,
    }
	
    Response := &greeting.Response{}
    
    gkBoot.DoRequest("http://localhost:8080", Request, Response)
    
    //Response contains "Hello, Simon! You're 21!"
}
```
