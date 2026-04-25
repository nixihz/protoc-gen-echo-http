package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedCodeUsesQualifiedExternalTypes(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/common/common.proto", `syntax = "proto3";
package review.common;
option go_package = "example.com/review/common;common";

message Empty {}
message User { string id = 1; }
`)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "common/common.proto";
import "google/api/annotations.proto";

service UserService {
  rpc GetUser (review.common.Empty) returns (review.common.User) {
    option (google.api.http) = {
      get: "/v1/users"
    };
  }
}
`)

	runProtoc(t, tmpDir, "common/common.proto", "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, `common "example.com/review/common"`)
	assertContains(t, generated, `GetUser(ctx context.Context, req *common.Empty) (*common.User, error)`)
	assertContains(t, generated, `var req common.Empty`)
	// Empty message has no fields, so binding code should not try to set req.Id.
	assertNotContains(t, generated, `req.Id = v`)
}

func TestGeneratedErrorTypesAreScopedByGoImportPath(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/a/a.proto", `syntax = "proto3";
package review.a;
option go_package = "example.com/review/a;api";

import "google/api/annotations.proto";

service AService {
  rpc GetA (GetARequest) returns (A) {
    option (google.api.http) = {
      get: "/v1/a"
    };
  }
}
message GetARequest { string id = 1; }
message A { string id = 1; }
`)
	writeFile(t, tmpDir, "proto/b/b.proto", `syntax = "proto3";
package review.b;
option go_package = "example.com/review/b;api";

import "google/api/annotations.proto";

service BService {
  rpc GetB (GetBRequest) returns (B) {
    option (google.api.http) = {
      get: "/v1/b"
    };
  }
}
message GetBRequest { string id = 1; }
message B { string id = 1; }
`)

	runProtoc(t, tmpDir, "a/a.proto", "b/b.proto")

	assertContains(t, readFile(t, tmpDir, "out/a/a_echo.pb.go"), "type APIError struct")
	assertContains(t, readFile(t, tmpDir, "out/b/b_echo.pb.go"), "type APIError struct")
}

func TestGeneratedCodeHandlesNumericPathParams(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users/{user_id}"
    };
  }
}

message GetUserRequest {
  int32 user_id = 1;
  string filter = 2;
}
message User { string id = 1; string name = 2; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	// int32 path param should use strconv.ParseInt
	assertContains(t, generated, `strconv.ParseInt`)
	assertContains(t, generated, `req.UserId = int32(parsed)`)
	// string query param should be directly assigned (no strconv)
	assertContains(t, generated, `c.QueryParam("filter")`)
	assertContains(t, generated, `req.Filter = v`)
}

func TestGoogleAPIHTTPAnnotationIsUsed(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc UpdateUser (UpdateUserRequest) returns (User) {
    option (google.api.http) = {
      patch: "/v1/users/{user_id}"
      body: "user"
    };
  }
}

message UpdateUserRequest {
  string user_id = 1;
  User user = 2;
  string filter = 3;
}
message User { string id = 1; string name = 2; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, `UpdateUser handles PATCH /v1/users/{user_id}`)
	assertContains(t, generated, `c.Param("user_id")`)
	assertContains(t, generated, `req.UserId = v`)
	assertContains(t, generated, `c.QueryParam("filter")`)
	assertContains(t, generated, `r.PATCH("/v1/users/:user_id", userServiceAdapter.UpdateUser)`)
}

func TestGeneratedCodePreservesV1Prefix(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/user/{id}"
    };
  }
}

message GetUserRequest { string id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, `r.GET("/v1/user/:id", userServiceAdapter.GetUser)`)
}

func TestGeneratedGETSkipsBind(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/user/{id}"
    };
  }
}

message GetUserRequest { string id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	// GET requests should skip c.Bind and only bind path/query params
	assertNotContains(t, generated, `c.Bind(&req)`)
	assertContains(t, generated, `c.Param("id")`)
}

func TestGeneratedPOSTUsesBodyBinding(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc CreateUser (CreateUserRequest) returns (User) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
}

message CreateUserRequest { string name = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	// POST with body="*" should bind body and skip query params
	assertContains(t, generated, `c.Bind(&req)`)
	assertNotContains(t, generated, `c.QueryParam("name")`)
}

func TestGeneratedCodeHasStatusCodeMethod(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users"
    };
  }
}

message GetUserRequest { string id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, "func (e *APIError) StatusCode() int")
	assertContains(t, generated, "return e.Code")
}

func TestGeneratedCodeHasErrorHandler(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users"
    };
  }
}

message GetUserRequest { string id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, "func RegisterAPIErrorHandler(e *echo.Echo)")
}

func TestGeneratedCodeUsesRouterInterface(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users"
    };
  }
}

message GetUserRequest { string id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertNotContains(t, generated, "g *echo.Group")
	assertContains(t, generated, "r interface {")
	assertContains(t, generated, "GET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo")
}

func TestGeneratedCodeUsesHTTPStatusConstants(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users/{id}"
    };
  }
}

message GetUserRequest { int32 id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, `Code: http.StatusBadRequest`)
	assertContains(t, generated, `Code: http.StatusNotFound`)
	assertContains(t, generated, `return c.JSON(http.StatusOK, resp)`)
}

func TestGeneratedNumericParamReturnsErrorOnInvalidInput(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)
	writeFile(t, tmpDir, "proto/api/user.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users/{id}"
    };
  }
}

message GetUserRequest { int32 id = 1; }
message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/user.proto")

	generated := readFile(t, tmpDir, "out/api/user_echo.pb.go")
	assertContains(t, generated, `parsed, err := strconv.ParseInt(v, 10, 32)`)
	assertContains(t, generated, `if err != nil {`)
	assertContains(t, generated, `return echo.NewHTTPError(http.StatusBadRequest, err.Error())`)
}

func buildPlugin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skipf("protoc not found: %v", err)
	}
	if _, err := exec.LookPath("protoc-gen-go"); err != nil {
		t.Skipf("protoc-gen-go not found: %v", err)
	}

	tmpDir := t.TempDir()
	pluginPath := filepath.Join(tmpDir, "protoc-gen-echo-http")
	cmd := exec.Command("go", "build", "-o", pluginPath, ".")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}
	return tmpDir
}

func runProtoc(t *testing.T, tmpDir string, protoFiles ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(tmpDir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-I", filepath.Join(tmpDir, "proto"),
		"--plugin=protoc-gen-echo-http=" + filepath.Join(tmpDir, "protoc-gen-echo-http"),
		"--go_out=" + filepath.Join(tmpDir, "out"),
		"--go_opt=paths=source_relative",
		"--echo-http_out=" + filepath.Join(tmpDir, "out"),
		"--echo-http_opt=paths=source_relative",
	}
	for _, protoFile := range protoFiles {
		args = append(args, filepath.Join(tmpDir, "proto", protoFile))
	}

	cmd := exec.Command("protoc", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("protoc: %v\n%s", err, output)
	}
}

func writeGoogleAPIProtos(t *testing.T, tmpDir string) {
	t.Helper()
	writeFile(t, tmpDir, "proto/google/api/http.proto", `syntax = "proto3";
package google.api;
option go_package = "google.golang.org/genproto/googleapis/api/annotations;annotations";

message HttpRule {
  oneof pattern {
    string get = 2;
    string put = 3;
    string post = 4;
    string delete = 5;
    string patch = 6;
    CustomHttpPattern custom = 8;
  }
  string body = 7;
  repeated HttpRule additional_bindings = 11;
}

message CustomHttpPattern {
  string kind = 1;
  string path = 2;
}
`)
	writeFile(t, tmpDir, "proto/google/api/annotations.proto", `syntax = "proto3";
package google.api;
option go_package = "google.golang.org/genproto/googleapis/api/annotations;annotations";

import "google/api/http.proto";
import "google/protobuf/descriptor.proto";

extend google.protobuf.MethodOptions {
  HttpRule http = 72295728;
}
`)
}

func writeFile(t *testing.T, baseDir string, name string, content string) {
	t.Helper()
	path := filepath.Join(baseDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, baseDir string, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baseDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func assertContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("generated code does not contain %q:\n%s", needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("generated code unexpectedly contains %q:\n%s", needle, haystack)
	}
}

func TestGeneratedCodeCompiles(t *testing.T) {
	tmpDir := buildPlugin(t)
	writeGoogleAPIProtos(t, tmpDir)

	// Write a comprehensive proto that exercises all code paths
	writeFile(t, tmpDir, "proto/api/service.proto", `syntax = "proto3";
package review.api;
option go_package = "example.com/review/api;api";

import "google/api/annotations.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users/{user_id}"
    };
  }
  rpc CreateUser (CreateUserRequest) returns (User) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
  rpc UpdateUser (UpdateUserRequest) returns (User) {
    option (google.api.http) = {
      patch: "/v1/users/{user_id}"
      body: "user"
    };
  }
}

message GetUserRequest {
  int32 user_id = 1;
  string filter = 2;
}

message CreateUserRequest {
  string name = 1;
}

message UpdateUserRequest {
  string user_id = 1;
  User user = 2;
  string note = 3;
}

message User { string id = 1; }
`)

	runProtoc(t, tmpDir, "api/service.proto")

	outDir := filepath.Join(tmpDir, "out")

	// Create go.mod for the generated module
	goMod := `module example.com/review/api

go 1.25

require (
	github.com/labstack/echo/v5 v5.0.4
	google.golang.org/genproto/googleapis/api v0.0.0-20260420184626-e10c466a9529
	google.golang.org/protobuf v1.36.11
)
`
	writeFile(t, tmpDir, "out/go.mod", goMod)

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = outDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, output)
	}

	cmd = exec.Command("go", "build", "./...")
	cmd.Dir = outDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
}
