# protoc-gen-echo-http

A protoc plugin that generates Echo HTTP handler interfaces from protobuf service definitions.

## Features

- Auto-generates Go HTTP handler interfaces from protobuf `Service` definitions
- Supports automatic route mapping (Get, List, Create, Update, Delete, Login, Register, etc.)
- Generates Echo framework adapters for direct integration with Echo routes
- Supports automatic path parameter binding

## Installation

### Install from GitHub (Recommended)

```bash
go install github.com/nixihz/protoc-gen-echo-http@latest
```

### Install Locally

```bash
go install .
```

## Usage

### 1. Define Proto Service

```protobuf
syntax = "proto3";

package example;

service UserService {
  rpc GetUser (GetUserRequest) returns (User);
  rpc ListUsers (ListUsersRequest) returns (ListUsersResponse);
  rpc CreateUser (CreateUserRequest) returns (User);
  rpc UpdateUser (UpdateUserRequest) returns (User);
  rpc DeleteUser (DeleteUserRequest) returns (Empty);
  rpc Login (LoginRequest) returns (LoginResponse);
}

message GetUserRequest {
  string id = 1;
}

message User {
  string id = 1;
  string name = 2;
  string email = 3;
}
// ... other message definitions
```

### 2. Generate Code

```bash
protoc --echo-http_out=. --echo-http_opt=paths=source_relative your.proto
```

### 3. Implement Handler

The generated code creates a `UserHandler` interface and adapter. You just need to implement the interface:

```go
type UserHandlerImpl struct{}

func (h *UserHandlerImpl) GetUser(ctx context.Context, req *proto.GetUserRequest) (*proto.User, error) {
    // Implement your business logic
    return &proto.User{Id: req.Id, Name: "John"}, nil
}

func (h *UserHandlerImpl) ListUsers(ctx context.Context, req *proto.ListUsersRequest) (*proto.ListUsersResponse, error) {
    // Implement your business logic
    return &proto.ListUsersResponse{Users: []*proto.User{}}, nil
}
```

### 4. Register Routes

```go
e := echo.New()

userHandler := &UserHandlerImpl{}
proto.RegisterUserServiceHandlers(e.Group("/users"), userHandler)
```

## Auto Route Mapping Rules

| Method Name | HTTP Method | Path |
|-------------|-------------|------|
| GetXxx / GetXxxById | GET | /xxx/{id} |
| ListXxx | GET | /xxx |
| CreateXxx | POST | /xxx |
| UpdateXxx | PUT | /xxx/{id} |
| DeleteXxx | DELETE | /xxx/{id} |
| Login | POST | /xxx/login |
| Register | POST | /xxx/register |
| Others | GET | /xxx |

## Available Commands

### Taskfile Commands

```bash
# Build the plugin
task build

# Install plugin to $GOPATH/bin
task install

# Clean build artifacts
task clean
```

## Requirements

- Go 1.25+
- protoc
- google.golang.org/protobuf v1.36.1
- github.com/labstack/echo/v5

## Generated Code

Running the plugin generates `_echo.pb.go` files containing:

1. **Handler Interface** - e.g., `UserHandler`, defines all HTTP methods
2. **Adapter** - e.g., `UserHandlerAdapter`, converts interface to Echo handler
3. **Registration Function** - e.g., `RegisterUserServiceHandlers`, for easy route registration
