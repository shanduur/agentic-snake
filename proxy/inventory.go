// Copyright 2026 Mateusz Urbanek.

package proxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validate before ClientSession.ListTools silently excludes malformed entries.
// A filtered page is not a complete inventory and must not replace a catalog.
func inventoryValidationMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil {
			return result, err
		}
		if list, ok := result.(*mcp.ListToolsResult); ok {
			if err := validateRawInventory(list.Tools); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
}

func validateRawInventory(tools []*mcp.Tool) (err error) {
	// AddTool is the SDK's descriptor/header validator. Contain only its
	// documented validation panic, not network handling or gateway publication.
	defer func() {
		if recover() != nil {
			err = errors.New("upstream returned an invalid tool descriptor")
		}
	}()
	validator := mcp.NewServer(&mcp.Implementation{
		Name:    "inventory-validator",
		Version: "1",
	}, nil)
	for _, tool := range tools {
		if tool == nil {
			return errors.New("upstream returned a null tool descriptor")
		}
		if err := validateToolSchema(tool.InputSchema); err != nil {
			return fmt.Errorf("upstream tool %q input schema: %w", tool.Name, err)
		}
		if tool.OutputSchema != nil {
			if err := validateToolSchema(tool.OutputSchema); err != nil {
				return fmt.Errorf("upstream tool %q output schema: %w", tool.Name, err)
			}
		}
		validator.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	return nil
}

// The MCP tool contract requires an object at the root. Nested schemas may
// still be boolean or use any standard JSON Schema 2020-12 construct.
func validateToolSchema(raw any) error {
	root, ok := raw.(map[string]any)
	if !ok || root["type"] != "object" {
		return errors.New("root must have type object")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// Only compiler-provided metaschemas and this document are available.
	// No peer-controlled URL may read local files or fetch network resources.
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	const schemaURL = "https://gateway.invalid/tool-schema"
	if err := compiler.AddResource(schemaURL, root); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	if _, err := compiler.Compile(schemaURL); err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	return nil
}
