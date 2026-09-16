package script

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const scriptFilename = "acquisition_script.star"

// The standard library is deliberately reduced to the small set of pure
// helpers useful for a protocol script.  In particular, reflection-like
// helpers and the mutable set type are not part of the script language.
var allowedUniverseNames = map[string]struct{}{
	"None":      {},
	"False":     {},
	"True":      {},
	"abs":       {},
	"all":       {},
	"any":       {},
	"bool":      {},
	"bytes":     {},
	"chr":       {},
	"dict":      {},
	"enumerate": {},
	"float":     {},
	"int":       {},
	"len":       {},
	"list":      {},
	"max":       {},
	"min":       {},
	"ord":       {},
	"print":     {},
	"range":     {},
	"repr":      {},
	"reversed":  {},
	"sorted":    {},
	"str":       {},
	"tuple":     {},
	"zip":       {},
}

var deniedUniverseNames = map[string]string{
	"dir":     "attribute introspection is disabled",
	"getattr": "dynamic attribute access is disabled",
	"hasattr": "dynamic attribute access is disabled",
	"hash":    "object introspection is disabled",
	"set":     "sets are disabled",
	"type":    "type introspection is disabled",
}

// Validate parses and compiles source using the default controlled language
// and resource limits.  It never initializes or invokes the resulting
// program, and therefore cannot access a Host.
func Validate(source string, limits ...Limits) error {
	var configured Limits
	if len(limits) > 0 {
		configured = limits[0]
	}
	_, err := compileVersion(ScriptVersion{Source: source}, configured.withDefaults())
	return err
}

// ValidateScript is the named variant used by service adapters.
func ValidateScript(source string, limits Limits) error {
	return Validate(source, limits)
}

// Compile builds an immutable program without a Runtime cache.  Runtime.Compile
// should be preferred by the acquisition service so published versions share a
// cache while retaining version/checksum isolation.
func Compile(version ScriptVersion, limits ...Limits) (*CompiledScript, error) {
	var configured Limits
	if len(limits) > 0 {
		configured = limits[0]
	}
	return compileVersion(version, configured.withDefaults())
}

// CompileSource is a convenience for callers that do not yet have a
// persistence-layer ScriptVersion identity.
func CompileSource(source string, limits ...Limits) (*CompiledScript, error) {
	return Compile(ScriptVersion{Source: source}, limits...)
}

func compileVersion(version ScriptVersion, limits Limits) (*CompiledScript, error) {
	sourceBytes := len([]byte(version.Source))
	if sourceBytes > limits.MaxSourceBytes {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("source is %d bytes, maximum is %d", sourceBytes, limits.MaxSourceBytes),
			nil,
		)
	}
	if !utf8.ValidString(version.Source) {
		return nil, newClassifiedError(ErrorClassScriptCompile, "source must be valid UTF-8", nil)
	}

	checksum := sourceChecksum(version.Source)
	if version.Checksum != "" && !strings.EqualFold(version.Checksum, checksum) {
		return nil, newClassifiedError(
			ErrorClassScriptCompile,
			"published checksum does not match source",
			nil,
		)
	}
	version.Checksum = checksum

	options := &syntax.FileOptions{}
	file, err := options.Parse(scriptFilename, version.Source, 0)
	if err != nil {
		return nil, classifySourceError(err)
	}
	if err := validateSyntax(file); err != nil {
		return nil, err
	}
	if err := validateEntrypoint(file); err != nil {
		return nil, err
	}

	predeclared := compilePredeclared()
	program, err := starlark.FileProgram(file, predeclared.Has)
	if err != nil {
		return nil, classifySourceError(err)
	}
	if program.NumLoads() != 0 {
		// The AST check above is the normal path. Keep this guard in case the
		// interpreter changes its AST representation in a future dependency.
		return nil, newClassifiedError(ErrorClassScriptCompile, "load() is disabled", nil)
	}

	return &CompiledScript{
		version:     version,
		checksum:    checksum,
		sourceBytes: sourceBytes,
		program:     program,
	}, nil
}

func sourceChecksum(source string) string {
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])
}

func compilePredeclared() starlark.StringDict {
	predeclared := make(starlark.StringDict, len(allowedUniverseNames))
	for name := range allowedUniverseNames {
		value, ok := starlark.Universe[name]
		if !ok {
			// The dependency's standard universe is fixed, but keeping this
			// failure explicit avoids compiling a program with a nil binding if
			// an embedding application mutates Universe unexpectedly.
			continue
		}
		predeclared[name] = value
	}
	// print is overridden per invocation by a fresh builtin that accounts for
	// the invocation's print budget and metadata.
	predeclared["print"] = starlark.NewBuiltin("print", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return starlark.None, nil
	})
	return predeclared
}

func validateSyntax(file *syntax.File) error {
	var validationErr *Error
	syntax.Walk(file, func(node syntax.Node) bool {
		if validationErr != nil {
			return false
		}
		switch node := node.(type) {
		case *syntax.LoadStmt:
			validationErr = positionedCompileError(node.Load, "load() and module loading are disabled")
		case *syntax.WhileStmt:
			validationErr = positionedCompileError(node.While, "while loops are disabled")
		case *syntax.Ident:
			if reason, denied := deniedUniverseNames[node.Name]; denied {
				validationErr = positionedCompileError(node.NamePos, reason)
			} else if _, standard := starlark.Universe[node.Name]; standard {
				if _, allowed := allowedUniverseNames[node.Name]; !allowed {
					validationErr = positionedCompileError(node.NamePos, fmt.Sprintf("predeclared symbol %q is not allowed", node.Name))
				}
			}
		}
		return validationErr == nil
	})
	if validationErr == nil {
		return nil
	}
	return validationErr
}

func validateEntrypoint(file *syntax.File) error {
	var entrypoint *syntax.DefStmt
	var invalidBinding syntax.Node
	for _, statement := range file.Stmts {
		switch statement := statement.(type) {
		case *syntax.DefStmt:
			if statement.Name.Name == "after_poll" {
				if entrypoint != nil {
					return positionedCompileError(syntax.Start(statement), "after_poll must be defined exactly once")
				}
				entrypoint = statement
			}
		case *syntax.AssignStmt:
			if ident, ok := statement.LHS.(*syntax.Ident); ok && ident.Name == "after_poll" {
				invalidBinding = ident
			}
		}
	}

	if entrypoint == nil {
		if invalidBinding != nil {
			return positionedCompileError(syntax.Start(invalidBinding), "after_poll must be defined as def after_poll(ctx)")
		}
		return positionedCompileError(fileStart(file), "missing required entrypoint after_poll(ctx)")
	}
	if invalidBinding != nil {
		return positionedCompileError(syntax.Start(invalidBinding), "after_poll must be defined as def after_poll(ctx)")
	}
	if len(entrypoint.Params) != 1 {
		return positionedCompileError(entrypoint.Lparen, "after_poll must accept exactly one ctx parameter")
	}
	if _, ok := entrypoint.Params[0].(*syntax.Ident); !ok {
		return positionedCompileError(syntax.Start(entrypoint.Params[0]), "after_poll parameter must be a plain identifier")
	}
	return nil
}

func fileStart(file *syntax.File) syntax.Position {
	if len(file.Stmts) > 0 {
		return syntax.Start(file.Stmts[0])
	}
	return syntax.MakePosition(&file.Path, 1, 1)
}
