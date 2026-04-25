package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
				// Use f.Desc.Path() to get the proto file path (e.g., "api/md5/admin.proto")
				// The output filename should be relative to the proto file's directory,
				// matching the behavior of protoc-gen-go with paths=source_relative
				protoPath := f.Desc.Path()
				protoDir := filepath.Dir(protoPath)
				protoName := filepath.Base(protoPath)
				baseName := strings.TrimSuffix(protoName, filepath.Ext(protoName))
				outputFilename := filepath.Join(protoDir, baseName+"_echo.pb.go")

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
				errorTypesKey := string(goImportPath)
				if errorTypesKey == "" {
					errorTypesKey = outputFilename + ":" + goPackage
				}
				generateHandlerInterfaces(g, services, goPackage, errorTypesKey, generatedErrorTypes, *errorPkg, *errorType)
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
	PathParams  []paramBinding
	QueryParams []paramBinding
	InputIdent  protogen.GoIdent
	OutputIdent protogen.GoIdent
	ProtoMethod *protogen.Method // for field type lookup
}

type paramBinding struct {
	Name        string
	FieldGoName string
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
		if httpMethod == "" {
			continue
		}
		pathParams := extractPathParamBindings(method, path)
		queryParams := extractQueryParamBindings(method, path, body)

		info.Methods = append(info.Methods, methodInfo{
			GoName:      method.GoName,
			HTTPMethod:  httpMethod,
			Path:        path,
			PathParams:  pathParams,
			QueryParams: queryParams,
			InputIdent:  method.Input.GoIdent,
			OutputIdent: method.Output.GoIdent,
			ProtoMethod: method,
		})
	}

	return info
}

func extractPathParams(path string) []string {
	var params []string
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			param := part[1 : len(part)-1]
			params = append(params, strings.Split(param, "=")[0])
		}
	}
	return params
}

func extractPathParamBindings(method *protogen.Method, path string) []paramBinding {
	fieldNames := allFieldNames(method)
	var bindings []paramBinding
	for _, param := range extractPathParams(path) {
		if fieldGoName, ok := fieldNames[param]; ok {
			bindings = append(bindings, paramBinding{Name: param, FieldGoName: fieldGoName})
		}
	}
	return bindings
}

func extractQueryParamBindings(method *protogen.Method, path string, body string) []paramBinding {
	if body == "*" {
		return nil
	}

	pathParams := make(map[string]bool)
	for _, param := range extractPathParams(path) {
		pathParams[param] = true
	}

	fieldNames := allFieldNames(method)
	var bindings []paramBinding
	for _, field := range method.Input.Fields {
		fieldName := string(field.Desc.Name())
		if pathParams[fieldName] || fieldName == body {
			continue
		}
		if fieldGoName, ok := fieldNames[fieldName]; ok {
			bindings = append(bindings, paramBinding{Name: fieldName, FieldGoName: fieldGoName})
		}
	}
	return bindings
}

func allFieldNames(method *protogen.Method) map[string]string {
	fieldNames := make(map[string]string)
	for _, field := range method.Input.Fields {
		fieldNames[string(field.Desc.Name())] = field.GoName
	}
	return fieldNames
}

func isStreamingMethod(method *protogen.Method) bool {
	desc := method.Desc
	return desc.IsStreamingClient() || desc.IsStreamingServer()
}

func getFieldKind(method *protogen.Method, fieldName string) protoreflect.Kind {
	for _, field := range method.Input.Fields {
		if string(field.Desc.Name()) == fieldName {
			return field.Desc.Kind()
		}
	}
	return protoreflect.Kind(0)
}

func needsStrconvParam(methods []methodInfo) bool {
	for _, m := range methods {
		for _, p := range m.PathParams {
			kind := getFieldKind(m.ProtoMethod, p.Name)
			if kind == protoreflect.Int32Kind || kind == protoreflect.Int64Kind ||
				kind == protoreflect.Uint32Kind || kind == protoreflect.Uint64Kind ||
				kind == protoreflect.FloatKind || kind == protoreflect.DoubleKind {
				return true
			}
		}
		for _, p := range m.QueryParams {
			kind := getFieldKind(m.ProtoMethod, p.Name)
			if kind == protoreflect.Int32Kind || kind == protoreflect.Int64Kind ||
				kind == protoreflect.Uint32Kind || kind == protoreflect.Uint64Kind ||
				kind == protoreflect.FloatKind || kind == protoreflect.DoubleKind {
				return true
			}
		}
	}
	return false
}

func needsStrconvServices(services []serviceInfo) bool {
	for _, svc := range services {
		if needsStrconvParam(svc.Methods) {
			return true
		}
	}
	return false
}

func getHTTPBinding(method *protogen.Method) (methodName, path, body string) {
	return getGoogleAPIHTTPBinding(method)
}

// getGoogleAPIHTTPBinding parses google.api.http annotation.
func getGoogleAPIHTTPBinding(method *protogen.Method) (methodName, path, body string) {
	options := method.Desc.Options()
	if options == nil || !proto.HasExtension(options, annotations.E_Http) {
		return "", "", ""
	}

	ext := proto.GetExtension(options, annotations.E_Http)
	rule, ok := ext.(*annotations.HttpRule)
	if !ok || rule == nil {
		return "", "", ""
	}

	switch pattern := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		methodName, path = "GET", pattern.Get
	case *annotations.HttpRule_Put:
		methodName, path = "PUT", pattern.Put
	case *annotations.HttpRule_Post:
		methodName, path = "POST", pattern.Post
	case *annotations.HttpRule_Delete:
		methodName, path = "DELETE", pattern.Delete
	case *annotations.HttpRule_Patch:
		methodName, path = "PATCH", pattern.Patch
	case *annotations.HttpRule_Custom:
		if pattern.Custom != nil {
			methodName, path = pattern.Custom.Kind, pattern.Custom.Path
		}
	}

	if methodName != "" {
		return methodName, path, rule.Body
	}
	return "", "", ""
}

func toHandlerName(serviceName string) string {
	return strings.TrimSuffix(serviceName, "Service") + "Handler"
}

// Generate handler interfaces (not implementations)
func generateHandlerInterfaces(g *protogen.GeneratedFile, services []serviceInfo, pkgName string, errorTypesKey string, generatedErrorTypes map[string]bool, errorPkg string, errorType string) {
	g.P("// Code generated by protoc-gen-echo-http. DO NOT EDIT.")
	g.P("// versions:")
	g.P("// \tprotoc-gen-echo-http ", version)
	g.P()
	g.P("package ", pkgName)
	g.P()

	// Generate imports
	g.P("import (")
	g.P(`	"context"`)
	if !generatedErrorTypes[errorTypesKey] && (errorPkg == "" || errorType == "") {
		g.P(`	"errors"`)
	}
	g.P(`	"net/http"`)
	if needsStrconvServices(services) {
		g.P(`	"strconv"`)
	}
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
	if !generatedErrorTypes[errorTypesKey] {
		generateErrorTypes(g, errorPkg, errorType)
		if errorPkg == "" || errorType == "" {
			generateAPIErrorHandler(g)
		}
		generatedErrorTypes[errorTypesKey] = true
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
		g.P("\tErrBadRequest     = &", fullTypeName, "{Code: http.StatusBadRequest, Message: \"bad request\"}")
		g.P("\tErrUnauthorized  = &", fullTypeName, "{Code: http.StatusUnauthorized, Message: \"unauthorized\"}")
		g.P("\tErrForbidden     = &", fullTypeName, "{Code: http.StatusForbidden, Message: \"forbidden\"}")
		g.P("\tErrNotFound      = &", fullTypeName, "{Code: http.StatusNotFound, Message: \"not found\"}")
		g.P("\tErrInternalError = &", fullTypeName, "{Code: http.StatusInternalServerError, Message: \"internal server error\"}")
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
	g.P("// StatusCode implements echo.HTTPStatusCoder")
	g.P("func (e *APIError) StatusCode() int {")
	g.P("\treturn e.Code")
	g.P("}")
	g.P()
	g.P("var (")
	g.P("\tErrBadRequest     = &APIError{Code: http.StatusBadRequest, Message: \"bad request\"}")
	g.P("\tErrUnauthorized  = &APIError{Code: http.StatusUnauthorized, Message: \"unauthorized\"}")
	g.P("\tErrForbidden     = &APIError{Code: http.StatusForbidden, Message: \"forbidden\"}")
	g.P("\tErrNotFound      = &APIError{Code: http.StatusNotFound, Message: \"not found\"}")
	g.P("\tErrInternalError = &APIError{Code: http.StatusInternalServerError, Message: \"internal server error\"}")
	g.P(")")
	g.P()
}

func generateAPIErrorHandler(g *protogen.GeneratedFile) {
	g.P("// RegisterAPIErrorHandler registers a custom HTTP error handler on the Echo instance")
	g.P("// that returns APIError-formatted JSON responses with the correct HTTP status code.")
	g.P("func RegisterAPIErrorHandler(e *echo.Echo) {")
	g.P("\te.HTTPErrorHandler = func(c *echo.Context, err error) {")
	g.P("\t\tif resp, uErr := echo.UnwrapResponse(c.Response()); uErr == nil && resp.Committed {")
	g.P("\t\t\treturn")
	g.P("\t\t}")
	g.P()
	g.P("\t\tcode := http.StatusInternalServerError")
	g.P("\t\tvar sc echo.HTTPStatusCoder")
	g.P("\t\tif errors.As(err, &sc) && sc.StatusCode() != 0 {")
	g.P("\t\t\tcode = sc.StatusCode()")
	g.P("\t\t}")
	g.P()
	g.P("\t\tvar msg string")
	g.P("\t\tif he, ok := err.(*echo.HTTPError); ok {")
	g.P("\t\t\tmsg = he.Message")
	g.P("\t\t} else {")
	g.P("\t\t\tmsg = err.Error()")
	g.P("\t\t}")
	g.P()
	g.P("\t\tc.JSON(code, &APIError{Code: code, Message: msg})")
	g.P("\t}")
	g.P("}")
	g.P()
}

func generateHandlerInterface(g *protogen.GeneratedFile, svc serviceInfo, errorPkg string, errorType string) {
	// Generate handler interface
	g.P("// ", svc.HandlerGo, " defines the interface for ", svc.GoName, " HTTP handlers")
	g.P("type ", svc.HandlerGo, " interface {")
	for _, method := range svc.Methods {
		g.P("\t// ", method.GoName, " handles ", method.HTTPMethod, " ", method.Path)
		g.P("\t", method.GoName, "(ctx context.Context, req *", g.QualifiedGoIdent(method.InputIdent), ") (*", g.QualifiedGoIdent(method.OutputIdent), ", error)")
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
		g.P("\tvar req ", g.QualifiedGoIdent(method.InputIdent))
		if method.HTTPMethod != "GET" && method.HTTPMethod != "DELETE" {
			g.P("\tif err := c.Bind(&req); err != nil {")
			g.P("\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
			g.P("\t}")
			g.P()
		}

		// Bind path parameters
		for _, param := range method.PathParams {
			generateParamBinding(g, method.ProtoMethod, param, "c.Param")
		}
		g.P()

		// Bind query parameters (if any)
		if len(method.QueryParams) > 0 {
			for _, param := range method.QueryParams {
				generateParamBinding(g, method.ProtoMethod, param, "c.QueryParam")
			}
			g.P()
		}

		g.P("\tresp, err := a.Handler.", method.GoName, "(c.Request().Context(), &req)")
		g.P("\tif err != nil {")
		g.P("\t\treturn err")
		g.P("\t}")
		g.P()
		g.P("\treturn c.JSON(http.StatusOK, resp)")
		g.P("}")
		g.P()
	}
}

// getFieldType returns the protobuf type kind for a field by name in the input message.
func getFieldType(method *protogen.Method, fieldName string) protoreflect.Kind {
	input := method.Input
	for _, field := range input.Fields {
		if string(field.Desc.Name()) == fieldName {
			return field.Desc.Kind()
		}
	}
	return protoreflect.Kind(0) // InvalidKind
}

func generateRouteHelpers(g *protogen.GeneratedFile, services []serviceInfo, errorPkg string, errorType string) {
	for _, svc := range services {
		generateServiceRegistration(g, svc)
	}
}

func generateServiceRegistration(g *protogen.GeneratedFile, svc serviceInfo) {
	funcName := "Register" + svc.GoName + "Handlers"
	g.P("// ", funcName, " registers ", svc.GoName, " handlers to the router.")
	g.P("func ", funcName, "(")
	g.P("\tr interface {")
	g.P("\t\tGET(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo")
	g.P("\t\tPOST(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo")
	g.P("\t\tPUT(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo")
	g.P("\t\tDELETE(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo")
	g.P("\t\tPATCH(path string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) echo.RouteInfo")
	g.P("\t},")
	g.P("\th ", svc.HandlerGo, ",")
	g.P(") {")
	g.P()

	adapterName := toAdapterName(svc.GoName)
	g.P("\t", adapterName, " := &", svc.HandlerGo, "Adapter{Handler: h}")
	g.P()

	for _, method := range svc.Methods {
		path := convertPath(method.Path)
		g.P("\tr.", method.HTTPMethod, `("`, path, `", `, adapterName, ".", method.GoName, ")")
	}
	g.P("}")
	g.P()
}

func toAdapterName(serviceName string) string {
	if serviceName == "" {
		return "Adapter"
	}
	firstChar := strings.ToLower(string(serviceName[0]))
	return firstChar + serviceName[1:] + "Adapter"
}

func convertPath(path string) string {
	return convertPathParams(path)
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
			result.WriteString(echoParamName(inner))
		} else {
			result.WriteString(part)
		}
	}
	return result.String()
}

// generateParamBinding generates parameter binding code with type conversion.
func generateParamBinding(g *protogen.GeneratedFile, method *protogen.Method, param paramBinding, accessor string) {
	g.P("\tif v := ", accessor, `("`, echoParamName(param.Name), `"); v != "" {`)
	kind := getFieldKind(method, param.Name)
	switch kind {
	case protoreflect.Int32Kind:
		g.P("\t\tparsed, err := strconv.ParseInt(v, 10, 32)")
		g.P("\t\tif err != nil {")
		g.P("\t\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
		g.P("\t\t}")
		g.P("\t\treq.", param.FieldGoName, " = int32(parsed)")
	case protoreflect.Int64Kind:
		g.P("\t\tparsed, err := strconv.ParseInt(v, 10, 64)")
		g.P("\t\tif err != nil {")
		g.P("\t\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
		g.P("\t\t}")
		g.P("\t\treq.", param.FieldGoName, " = parsed")
	case protoreflect.Uint32Kind:
		g.P("\t\tparsed, err := strconv.ParseUint(v, 10, 32)")
		g.P("\t\tif err != nil {")
		g.P("\t\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
		g.P("\t\t}")
		g.P("\t\treq.", param.FieldGoName, " = uint32(parsed)")
	case protoreflect.Uint64Kind:
		g.P("\t\tparsed, err := strconv.ParseUint(v, 10, 64)")
		g.P("\t\tif err != nil {")
		g.P("\t\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
		g.P("\t\t}")
		g.P("\t\treq.", param.FieldGoName, " = parsed")
	case protoreflect.FloatKind:
		g.P("\t\tparsed, err := strconv.ParseFloat(v, 32)")
		g.P("\t\tif err != nil {")
		g.P("\t\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
		g.P("\t\t}")
		g.P("\t\treq.", param.FieldGoName, " = float32(parsed)")
	case protoreflect.DoubleKind:
		g.P("\t\tparsed, err := strconv.ParseFloat(v, 64)")
		g.P("\t\tif err != nil {")
		g.P("\t\t\treturn echo.NewHTTPError(http.StatusBadRequest, err.Error())")
		g.P("\t\t}")
		g.P("\t\treq.", param.FieldGoName, " = parsed")
	default:
		// String fields: direct assignment
		g.P("\t\treq.", param.FieldGoName, " = v")
	}
	g.P("\t}")
}

func echoParamName(param string) string {
	return strings.Split(param, "=")[0]
}

// version can be set via -ldflags during build
var version = "dev"
