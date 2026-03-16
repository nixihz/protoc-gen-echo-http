package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	var flags flag.FlagSet
	packageName := flags.String("package", "", "Generated package name (overrides proto package)")
	errorPkg := flags.String("error_pkg", "", "External error package path (e.g., zhiyu-server/pkg/apierror)")
	errorType := flags.String("error_type", "", "External error type name (e.g., APIError)")
	flags.Parse(os.Args[1:])

	// Track if error types have been generated (only generate once per package)
	generatedErrorTypes := make(map[string]bool)

	protogen.Options{
		ParamFunc: flags.Set,
	}.Run(func(gen *protogen.Plugin) error {
		gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

		for _, f := range gen.Files {
			if !f.Generate {
				continue
			}

			var services []serviceInfo
			for _, svc := range f.Services {
				services = append(services, extractServiceInfo(svc))
			}

			if len(services) > 0 {
				// Get the output path relative to the proto file's directory for source_relative mode
				// GeneratedFilenamePrefix is like "api/md5/admin" for "api/md5/admin.proto"
				outputFilename := f.GeneratedFilenamePrefix + "_echo.pb.go"

				// Determine package name from proto file path (directory name)
				// For paths=source_relative, use the directory name as package name
				goPackage := string(f.GoPackageName)
				goImportPath := f.GoImportPath

				// If GoPackageName is empty or proto (default), extract from proto path
				if goPackage == "" || goPackage == "proto" {
					// Extract directory from proto path (e.g., "api/md5/admin.proto" -> "md5")
					protoPath := f.Desc.Path()
					dir := filepath.Dir(protoPath)
					if dir != "." {
						goPackage = filepath.Base(dir)
					}
				}

				// Override with provided package name if specified
				if *packageName != "" {
					goPackage = *packageName
				}

				g := gen.NewGeneratedFile(outputFilename, goImportPath)
				generateHandlerInterfaces(g, services, goPackage, generatedErrorTypes, *errorPkg, *errorType)
			}
		}

		return nil
	})
}

type serviceInfo struct {
	GoName    string
	Methods   []methodInfo
	HandlerGo string
}

type methodInfo struct {
	GoName      string
	HTTPMethod  string
	Path        string
	Body        string
	HasBody     bool
	PathParams  []string
	QueryParams []string
	InputName   string
	OutputName  string
}

func extractServiceInfo(svc *protogen.Service) serviceInfo {
	info := serviceInfo{
		GoName:    svc.GoName,
		HandlerGo: toHandlerName(svc.GoName),
	}

	for _, method := range svc.Methods {
		if isStreamingMethod(method) {
			continue
		}

		httpMethod, path, body := getHTTPBinding(method)
		pathParams := extractPathParams(path)
		queryParams := extractQueryParams(method)

		info.Methods = append(info.Methods, methodInfo{
			GoName:      method.GoName,
			HTTPMethod:  httpMethod,
			Path:        path,
			Body:        body,
			HasBody:     body != "" && body != "*",
			PathParams:  pathParams,
			QueryParams: queryParams,
			InputName:   method.Input.GoIdent.GoName,
			OutputName:  method.Output.GoIdent.GoName,
		})
	}

	return info
}

func extractPathParams(path string) []string {
	var params []string
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "{") {
			param := strings.Trim(part, "{}")
			params = append(params, strings.Split(param, "=")[0])
		}
	}
	return params
}

func extractQueryParams(method *protogen.Method) []string {
	// TODO: Implement google.api.http annotation parsing for query parameters
	// This requires parsing the method options to extract query fields
	return nil
}

func isStreamingMethod(method *protogen.Method) bool {
	desc := method.Desc
	return desc.IsStreamingClient() || desc.IsStreamingServer()
}

func getHTTPBinding(method *protogen.Method) (methodName, path, body string) {
	// First try to get from google.api.http annotation
	if httpMethod, httpPath, httpBody := getGoogleAPIHTTPBinding(method); httpMethod != "" {
		return httpMethod, httpPath, httpBody
	}
	// Fallback to auto mapping
	m, p := getHTTPMapping(method)
	return m, p, ""
}

// getGoogleAPIHTTPBinding parses google.api.http annotation
// TODO: Implement full annotation parsing
func getGoogleAPIHTTPBinding(method *protogen.Method) (methodName, path, body string) {
	// Placeholder for future implementation
	// Would need to parse the google.api.http extension
	return "", "", ""
}

// getHTTPMapping is fallback when no google.api.http annotation
func getHTTPMapping(method *protogen.Method) (methodName, path string) {
	name := method.GoName
	serviceName := method.Parent.GoName
	resourceName := strings.TrimSuffix(serviceName, "Service")
	pluralResourceName := pluralize(resourceName)

	switch {
	case name == "Login":
		return "POST", "/v1/" + toSnakeCase(pluralResourceName) + "/login"
	case name == "Register":
		return "POST", "/v1/" + toSnakeCase(pluralResourceName) + "/register"
	case name == "Get"+resourceName || name == "Get"+resourceName+"ById":
		return "GET", "/v1/" + toSnakeCase(pluralResourceName) + "/{id}"
	case strings.HasPrefix(name, "Get"):
		return "GET", "/v1/" + toSnakeCase(pluralize(strings.TrimPrefix(name, "Get")))
	case strings.HasPrefix(name, "List"):
		return "GET", "/v1/" + toSnakeCase(pluralize(strings.TrimPrefix(name, "List")))
	case strings.HasPrefix(name, "Create"):
		return "POST", "/v1/" + toSnakeCase(pluralize(strings.TrimPrefix(name, "Create")))
	case strings.HasPrefix(name, "Update"):
		return "PUT", "/v1/" + toSnakeCase(pluralize(strings.TrimPrefix(name, "Update"))) + "/{id}"
	case strings.HasPrefix(name, "Delete"):
		return "DELETE", "/v1/" + toSnakeCase(pluralize(strings.TrimPrefix(name, "Delete"))) + "/{id}"
	default:
		return "GET", "/v1/" + toSnakeCase(name)
	}
}

func toHandlerName(serviceName string) string {
	return strings.TrimSuffix(serviceName, "Service") + "Handler"
}

func pluralize(s string) string {
	if s == "" {
		return "items"
	}
	// Already plural
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "es") || strings.HasSuffix(s, "ies") {
		return s
	}
	last := s[len(s)-1]
	if last == 'y' && len(s) > 1 {
		return s[:len(s)-1] + "ies"
	}
	if last == 's' {
		return s + "es"
	}
	return s + "s"
}

func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// Generate handler interfaces (not implementations)
func generateHandlerInterfaces(g *protogen.GeneratedFile, services []serviceInfo, pkgName string, generatedErrorTypes map[string]bool, errorPkg string, errorType string) {
	g.P("// Code generated by protoc-gen-echo-http. DO NOT EDIT.")
	g.P("// versions:")
	g.P("// \tprotoc-gen-echo-http ", version)
	g.P()
	g.P("package ", pkgName)
	g.P()

	// Generate imports
	g.P("import (")
	g.P(`	"context"`)
	g.P(`	"github.com/labstack/echo/v5"`)
	if errorPkg != "" {
		// Extract package alias from path
		parts := strings.Split(errorPkg, "/")
		alias := parts[len(parts)-1]
		g.P("\t", alias, ` "`, errorPkg, `"`)
	}
	g.P(")")
	g.P()

	// Generate error types only once per package
	if !generatedErrorTypes[pkgName] {
		generateErrorTypes(g, errorPkg, errorType)
		generatedErrorTypes[pkgName] = true
	}
	g.P()

	// Generate handler interfaces for each service
	for _, svc := range services {
		generateHandlerInterface(g, svc, errorPkg, errorType)
	}

	// Generate route registration helper
	generateRouteHelpers(g, services, errorPkg, errorType)
}

func generateErrorTypes(g *protogen.GeneratedFile, errorPkg string, errorType string) {
	// Use external error type if configured - only generate error constants, not type definition
	if errorPkg != "" && errorType != "" {
		parts := strings.Split(errorPkg, "/")
		alias := parts[len(parts)-1]
		fullTypeName := alias + "." + errorType

		// Generate error constants using external type (no local type definition)
		g.P("// Error constants using external type from ", errorPkg)
		g.P("var (")
		g.P("\tErrBadRequest     = &", fullTypeName, "{Code: 400, Message: \"bad request\"}")
		g.P("\tErrUnauthorized  = &", fullTypeName, "{Code: 401, Message: \"unauthorized\"}")
		g.P("\tErrForbidden     = &", fullTypeName, "{Code: 403, Message: \"forbidden\"}")
		g.P("\tErrNotFound      = &", fullTypeName, "{Code: 404, Message: \"not found\"}")
		g.P("\tErrInternalError = &", fullTypeName, "{Code: 500, Message: \"internal server error\"}")
		g.P(")")
		g.P()
		return
	}

	// Default: generate built-in error types
	g.P("// APIError represents a structured API error")
	g.P("type APIError struct {")
	g.P("\tCode    int    `json:\"code\"`")
	g.P("\tMessage string `json:\"message\"`")
	g.P("}")
	g.P()
	g.P("func (e *APIError) Error() string {")
	g.P("\treturn e.Message")
	g.P("}")
	g.P()
	g.P("var (")
	g.P("\tErrBadRequest     = &APIError{Code: 400, Message: \"bad request\"}")
	g.P("\tErrUnauthorized  = &APIError{Code: 401, Message: \"unauthorized\"}")
	g.P("\tErrForbidden     = &APIError{Code: 403, Message: \"forbidden\"}")
	g.P("\tErrNotFound      = &APIError{Code: 404, Message: \"not found\"}")
	g.P("\tErrInternalError = &APIError{Code: 500, Message: \"internal server error\"}")
	g.P(")")
	g.P()
}

func generateHandlerInterface(g *protogen.GeneratedFile, svc serviceInfo, errorPkg string, errorType string) {
	// Determine the error type to use
	errType := "APIError"
	if errorPkg != "" && errorType != "" {
		parts := strings.Split(errorPkg, "/")
		alias := parts[len(parts)-1]
		errType = alias + "." + errorType
	}

	// Generate handler interface
	g.P("// ", svc.HandlerGo, " defines the interface for ", svc.GoName, " HTTP handlers")
	g.P("type ", svc.HandlerGo, " interface {")
	for _, method := range svc.Methods {
		g.P("\t// ", method.GoName, " handles ", method.HTTPMethod, " ", method.Path)
		g.P("\t", method.GoName, "(ctx context.Context, req *", method.InputName, ") (*", method.OutputName, ", error)")
	}
	g.P("}")
	g.P()

	// Generate adapter that wraps the interface as Echo handler
	g.P("// ", svc.HandlerGo, "Adapter adapts ", svc.HandlerGo, " to echo.HandlerFunc")
	g.P("type ", svc.HandlerGo, "Adapter struct {")
	g.P("\tHandler ", svc.HandlerGo)
	g.P("}")
	g.P()

	// Generate adapter methods
	for _, method := range svc.Methods {
		g.P("func (a *", svc.HandlerGo, "Adapter) ", method.GoName, "(c *echo.Context) error {")
		g.P("\tvar req ", method.InputName)
		g.P("\tif err := c.Bind(&req); err != nil {")
		g.P("\t\treturn c.JSON(400, &", errType, "{Code: 400, Message: err.Error()})")
		g.P("\t}")
		g.P()

		// Bind path parameters
		for _, param := range method.PathParams {
			fieldName := toFieldName(param)
			g.P("\tif v := c.Param(\"", param, "\"); v != \"\" {")
			g.P("\t\treq.", fieldName, " = v")
			g.P("\t}")
		}
		g.P()

		// Bind query parameters (if any)
		if len(method.QueryParams) > 0 {
			for _, param := range method.QueryParams {
				fieldName := toFieldName(param)
				g.P("\tif v := c.QueryParam(\"", param, "\"); v != \"\" {")
				g.P("\t\treq.", fieldName, " = v")
				g.P("\t}")
			}
			g.P()
		}

		g.P("\tresp, err := a.Handler.", method.GoName, "(c.Request().Context(), &req)")
		g.P("\tif err != nil {")
		g.P("\t\tswitch e := err.(type) {")
		if errorPkg != "" && errorType != "" {
			parts := strings.Split(errorPkg, "/")
			alias := parts[len(parts)-1]
			g.P("\t\tcase *", alias, ".", errorType, ":")
		} else {
			g.P("\t\tcase *APIError:")
		}
		g.P("\t\t\treturn c.JSON(e.Code, e)")
		g.P("\t\tdefault:")
		g.P("\t\t\treturn c.JSON(500, &", errType, "{Code: 500, Message: err.Error()})")
		g.P("\t\t}")
		g.P("\t}")
		g.P()
		g.P("\treturn c.JSON(200, resp)")
		g.P("}")
		g.P()
	}
}

func snakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	parts := strings.Split(s, "_")
	var resultBuilder strings.Builder
	resultBuilder.WriteString(strings.ToLower(parts[0]))
	for _, p := range parts[1:] {
		if len(p) == 0 {
			continue
		}
		resultBuilder.WriteString(strings.ToUpper(string(p[0])))
		resultBuilder.WriteString(strings.ToLower(p[1:]))
	}
	return resultBuilder.String()
}

func toFieldName(param string) string {
	camel := snakeToCamel(param)
	if len(camel) == 0 {
		return camel
	}
	// Convert first letter to uppercase for Go field names
	return strings.ToUpper(camel[:1]) + camel[1:]
}

func generateRouteHelpers(g *protogen.GeneratedFile, services []serviceInfo, errorPkg string, errorType string) {
	for _, svc := range services {
		generateServiceRegistration(g, svc)
	}
}

func generateServiceRegistration(g *protogen.GeneratedFile, svc serviceInfo) {
	funcName := "Register" + svc.GoName + "Handlers"
	g.P("// ", funcName, " registers ", svc.GoName, " handlers to the echo group.")
	g.P("func ", funcName, "(")
	g.P("\tg *echo.Group,")
	g.P("\th ", svc.HandlerGo, ",")
	g.P(") {")
	g.P()

	adapterName := toAdapterName(svc.GoName)
	g.P("\t", adapterName, " := &", svc.HandlerGo, "Adapter{Handler: h}")
	g.P()

	for _, method := range svc.Methods {
		path := convertPath(method.Path)
		g.P("\tg.", method.HTTPMethod, `("`, path, `", `, adapterName, ".", method.GoName, ")")
	}
	g.P("}")
	g.P()
}

func toAdapterName(serviceName string) string {
	firstChar := strings.ToLower(string(serviceName[0]))
	return firstChar + serviceName[1:] + "Adapter"
}

func convertPath(path string) string {
	// Remove /v1 prefix if present
	path = strings.TrimPrefix(path, "/v1")

	// Convert {id} to :id
	path = convertPathParams(path)

	return path
}

func convertPathParams(path string) string {
	var result strings.Builder
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if i > 0 {
			result.WriteString("/")
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			inner := part[1 : len(part)-1]
			result.WriteString(":")
			result.WriteString(snakeToCamel(inner))
		} else {
			result.WriteString(part)
		}
	}
	return result.String()
}

// version can be set via -ldflags during build
var version = "dev"
