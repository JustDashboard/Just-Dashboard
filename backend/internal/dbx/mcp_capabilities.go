package dbx

// What an AI client reaches through the MCP endpoint (api/handlers_mcp.go).
// Its tools are a schema read, a sample of a table, a plan and a statement,
// all through the dialects, so it is every engine with one; MongoDB and Redis
// speak a vocabulary of their own that the tools do not.
func init() {
	RegisterCapabilities(Capability{"mcp", sqlEngines})
}
