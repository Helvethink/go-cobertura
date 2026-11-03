package cobertura

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coverprofile "github.com/Helvethink/go-cobertura/internal/coverage"
	"golang.org/x/tools/go/packages"
)

const coberturaDTDDecl = `<!DOCTYPE coverage SYSTEM "http://cobertura.sourceforge.net/xml/coverage-04.dtd">`

type Options struct {
	Ignore     coverprofile.Ignore
	ByFiles    bool
	ProjectDir string
}

type conversionOptions = Options

func Convert(in io.Reader, out, warnings io.Writer, options Options) error {
	return convert(in, out, warnings, options)
}

func ConvertStrict(in io.Reader, out io.Writer, options Options) error {
	return convertStrict(in, out, options)
}

func convert(in io.Reader, out, warnings io.Writer, options conversionOptions) error {
	_, err := convertInternal(in, out, warnings, options)
	return err
}

// convertStrict is like convert but returns an error if any profiles were
// skipped or package loading produced diagnostics.
func convertStrict(in io.Reader, out io.Writer, options conversionOptions) error {
	var report bytes.Buffer
	result, err := convertInternal(in, &report, io.Discard, options)
	if err != nil {
		return err
	}
	if result.skipped > 0 || len(result.diagnostics) > 0 {
		parts := make([]string, 0, len(result.diagnostics)+1)
		if result.skipped > 0 {
			parts = append(parts, fmt.Sprintf("%d profile(s) skipped", result.skipped))
		}
		for _, diagnostic := range result.diagnostics {
			parts = append(parts, diagnostic.String())
		}
		return fmt.Errorf("strict conversion failed: %s", strings.Join(parts, "; "))
	}
	if _, err := io.Copy(out, &report); err != nil {
		return fmt.Errorf("writing strict coverage report: %w", err)
	}
	return nil
}

type packageLoadDiagnostic struct {
	packageID  string
	dependency string
	err        packages.Error
}

func (d packageLoadDiagnostic) String() string {
	if d.dependency != "" && d.dependency != d.packageID {
		return fmt.Sprintf("package %q via dependency %q: %s", d.packageID, d.dependency, d.err.Error())
	}
	return fmt.Sprintf("package %q: %s", d.packageID, d.err.Error())
}

type conversionResult struct {
	skipped     int
	diagnostics []packageLoadDiagnostic
}

func convertInternal(in io.Reader, out, warnings io.Writer, options conversionOptions) (conversionResult, error) {
	result := conversionResult{}
	profiles, err := coverprofile.ParseProfiles(in, &options.Ignore)
	if err != nil {
		return result, err
	}

	pkgs, diagnostics, err := getPackages(profiles, options.ProjectDir)
	if err != nil {
		return result, err
	}
	result.diagnostics = diagnostics
	for _, diagnostic := range diagnostics {
		if _, err := fmt.Fprintf(warnings, "warning: package load diagnostic: %s\n", diagnostic.String()); err != nil {
			return result, fmt.Errorf("writing package load diagnostic: %w", err)
		}
	}

	sources := make([]*Source, 0)
	pkgMap := make(map[string]*packages.Package)
	for _, pkg := range pkgs {
		// packages.Load can return packages with nil Module when the
		// package is from the standard library, is built in GOPATH mode,
		// or when dependency resolution fails (e.g. private repo auth).
		if pkg.Module == nil {
			continue
		}
		sources = appendIfUnique(sources, pkg.Module.Dir)
		pkgMap[pkg.ID] = pkg
	}

	coverage := Coverage{Sources: sources, Packages: nil, Timestamp: time.Now().UnixNano() / int64(time.Millisecond)}
	result.skipped, err = coverage.parseProfiles(profiles, pkgMap, diagnosticsByPackage(diagnostics), warnings, options)
	if err != nil {
		return result, err
	}

	if _, err := fmt.Fprint(out, xml.Header); err != nil {
		return result, fmt.Errorf("writing xml header: %w", err)
	}
	if _, err := fmt.Fprintln(out, coberturaDTDDecl); err != nil {
		return result, fmt.Errorf("writing cobertura dtd: %w", err)
	}

	encoder := xml.NewEncoder(out)
	encoder.Indent("", "  ")
	if err := encoder.Encode(coverage); err != nil {
		return result, fmt.Errorf("encoding coverage xml: %w", err)
	}

	if _, err := fmt.Fprintln(out); err != nil {
		return result, fmt.Errorf("writing trailing newline: %w", err)
	}
	return result, nil
}

func getPackages(profiles []*coverprofile.Profile, dir string) ([]*packages.Package, []packageLoadDiagnostic, error) {
	if len(profiles) == 0 {
		return []*packages.Package{}, nil, nil
	}

	pkgNames := make([]string, 0)
	seenPkgNames := make(map[string]bool)
	for _, profile := range profiles {
		pkgName := getPackageName(profile.FileName)
		if !seenPkgNames[pkgName] {
			pkgNames = append(pkgNames, pkgName)
			seenPkgNames[pkgName] = true
		}
	}
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:  dir,
	}, pkgNames...)
	if err != nil {
		return nil, nil, fmt.Errorf("loading packages: %w", err)
	}

	diagnostics := make([]packageLoadDiagnostic, 0)
	for _, root := range pkgs {
		packages.Visit([]*packages.Package{root}, func(pkg *packages.Package) bool {
			for _, pkgErr := range pkg.Errors {
				diagnostics = append(diagnostics, packageLoadDiagnostic{
					packageID:  root.ID,
					dependency: pkg.ID,
					err:        pkgErr,
				})
			}
			return true
		}, nil)
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].packageID != diagnostics[j].packageID {
			return diagnostics[i].packageID < diagnostics[j].packageID
		}
		if diagnostics[i].dependency != diagnostics[j].dependency {
			return diagnostics[i].dependency < diagnostics[j].dependency
		}
		if diagnostics[i].err.Pos != diagnostics[j].err.Pos {
			return diagnostics[i].err.Pos < diagnostics[j].err.Pos
		}
		if diagnostics[i].err.Msg != diagnostics[j].err.Msg {
			return diagnostics[i].err.Msg < diagnostics[j].err.Msg
		}
		return diagnostics[i].err.Kind < diagnostics[j].err.Kind
	})
	return pkgs, diagnostics, nil
}

func diagnosticsByPackage(diagnostics []packageLoadDiagnostic) map[string]bool {
	result := make(map[string]bool, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result[diagnostic.packageID] = true
	}
	return result
}

func appendIfUnique(sources []*Source, dir string) []*Source {
	for _, source := range sources {
		if source.Path == dir {
			return sources
		}
	}
	return append(sources, &Source{dir})
}

func getPackageName(filename string) string {
	pkgName, _ := path.Split(coverprofile.NormalizeImportPath(filename))
	return strings.TrimSuffix(pkgName, "/")
}

func readSourceFile(pkg *packages.Package, profileName, fileName string) (string, []byte, error) {
	filename := path.Base(profileName)
	for _, fullpath := range pkg.GoFiles {
		if filepath.Base(fullpath) == filename {
			data, err := os.ReadFile(fullpath)
			if err != nil {
				return "", nil, fmt.Errorf("reading source file %q: %w", profileName, err)
			}
			return fullpath, data, nil
		}
	}

	if pkg.Module.Dir == "" {
		return "", nil, fmt.Errorf("unable to determine file path for %s", profileName)
	}
	relativePath := filepath.FromSlash(fileName)
	if !filepath.IsLocal(relativePath) {
		return "", nil, fmt.Errorf("source path %q is outside module directory", fileName)
	}

	root, err := os.OpenRoot(pkg.Module.Dir)
	if err != nil {
		return "", nil, fmt.Errorf("opening module directory %q: %w", pkg.Module.Dir, err)
	}
	data, readErr := root.ReadFile(relativePath)
	closeErr := root.Close()
	if readErr != nil {
		return "", nil, fmt.Errorf("reading source file %q: %w", profileName, readErr)
	}
	if closeErr != nil {
		return "", nil, fmt.Errorf("closing module directory %q: %w", pkg.Module.Dir, closeErr)
	}

	return filepath.Join(pkg.Module.Dir, relativePath), data, nil
}

func (cov *Coverage) parseProfiles(
	profiles []*coverprofile.Profile,
	pkgMap map[string]*packages.Package,
	packageDiagnostics map[string]bool,
	warnings io.Writer,
	options conversionOptions,
) (int, error) {
	cov.Packages = []*Package{}
	skipped := 0
	for _, profile := range profiles {
		pkgName := getPackageName(profile.FileName)
		pkgPkg := pkgMap[pkgName]
		if pkgPkg == nil || pkgPkg.Module == nil {
			reason := "no package/module information available"
			if packageDiagnostics[pkgName] {
				reason = "package resolution failed; see package load diagnostic"
			}
			if _, err := fmt.Fprintf(
				warnings,
				"warning: skipping profile %s (package %q): %s\n",
				profile.FileName,
				pkgName,
				reason,
			); err != nil {
				return skipped, fmt.Errorf("writing skipped profile warning: %w", err)
			}
			skipped++
			continue
		}
		skip, err := cov.parseProfile(profile, pkgPkg, options)
		if err != nil {
			return skipped, err
		}
		if skip {
			skipped++
		}
	}
	cov.LinesValid = cov.NumLines()
	cov.LinesCovered = cov.NumLinesWithHits()
	cov.LineRate = cov.HitRate()
	return skipped, nil
}

func (cov *Coverage) parseProfile(profile *coverprofile.Profile, pkgPkg *packages.Package, options conversionOptions) (bool, error) {
	if pkgPkg == nil || pkgPkg.Module == nil {
		return true, nil
	}
	profileName := coverprofile.NormalizeImportPath(profile.FileName)
	modulePath := strings.TrimSuffix(coverprofile.NormalizeImportPath(pkgPkg.Module.Path), "/")
	if modulePath == "" {
		return false, fmt.Errorf("unable to resolve profile %q: module path is empty", profile.FileName)
	}
	fileName, ok := strings.CutPrefix(profileName, modulePath+"/")
	if !ok || fileName == "" {
		return false, fmt.Errorf("profile %q is outside module %q", profile.FileName, pkgPkg.Module.Path)
	}
	absFilePath, data, err := readSourceFile(pkgPkg, profileName, fileName)
	if err != nil {
		return false, err
	}

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, absFilePath, data, 0)
	if err != nil {
		return false, err
	}

	if options.Ignore.Match(fileName, data) {
		return false, nil
	}

	pkgPath := modulePath
	if pkgDir := path.Dir(fileName); pkgDir != "." {
		pkgPath = path.Join(modulePath, pkgDir)
	}

	var pkg *Package
	for _, p := range cov.Packages {
		if p.Name == pkgPath {
			pkg = p
		}
	}
	if pkg == nil {
		pkg = &Package{Name: pkgPath, Classes: []*Class{}}
		cov.Packages = append(cov.Packages, pkg)
	}

	// Get the set of valid lines, but only if we're ignoring non-code lines. We set up the struct
	// regardless so that the array on fileVisitor isn't nil. It shouldnt matter, but just in case!
	validVisitor := &validVisitor{
		fset:       fset,
		validLines: make(map[int]bool),
	}
	if options.Ignore.NonCodeLines {
		ast.Walk(validVisitor, parsed)
	}

	visitor := &fileVisitor{
		fset:               fset,
		fileName:           fileName,
		fileData:           data,
		classes:            make(map[string]*Class),
		pkg:                pkg,
		profile:            profile,
		validLines:         validVisitor.validLines,
		ignoreNonCodeLines: options.Ignore.NonCodeLines,
		byFiles:            options.ByFiles,
	}
	ast.Walk(visitor, parsed)
	pkg.LineRate = pkg.HitRate()
	return false, nil
}

type validVisitor struct {
	fset       *token.FileSet
	validLines map[int]bool
}

func (v *validVisitor) Visit(node ast.Node) ast.Visitor {
	// Add the line for this node to the list of valid lines.
	if node != nil {
		v.validLines[v.fset.Position(node.Pos()).Line] = true
	}
	return v
}

type fileVisitor struct {
	fset               *token.FileSet
	fileName           string
	fileData           []byte
	pkg                *Package
	classes            map[string]*Class
	profile            *coverprofile.Profile
	validLines         map[int]bool
	ignoreNonCodeLines bool
	byFiles            bool
}

func (v *fileVisitor) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.FuncDecl:
		class := v.classForFunc(n)
		method := v.methodFromNode(n, n.Name.Name)
		method.LineRate = method.Lines.HitRate()
		class.Methods = append(class.Methods, method)
		class.Lines = append(class.Lines, method.Lines...)
		class.LineRate = class.Lines.HitRate()
		// Return nil to stop descent — all lines in the function body
		// are already counted by methodFromNode using the Pos/End range.
		// This prevents local var closures from being double-counted.
		return nil
	case *ast.GenDecl:
		for _, spec := range n.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || len(vs.Values) == 0 {
				continue
			}
			for i, val := range vs.Values {
				funcLit, isFuncLit := val.(*ast.FuncLit)
				if !isFuncLit {
					continue
				}
				name := vs.Names[i].Name
				class := v.classForFunc(nil)
				// Use the FuncLit node for Pos/End so the range covers
				// the function body, not the entire var declaration.
				method := v.methodFromNode(funcLit, name)
				method.LineRate = method.Lines.HitRate()
				class.Methods = append(class.Methods, method)
				class.Lines = append(class.Lines, method.Lines...)
				class.LineRate = class.Lines.HitRate()
			}
		}
		return nil
	}
	return v
}

// methodFromNode creates a Method by scanning coverage blocks that overlap
// with the AST node's source range.
func (v *fileVisitor) methodFromNode(n ast.Node, name string) *Method {
	method := &Method{Name: name}
	method.Lines = []*Line{}

	start := v.fset.Position(n.Pos())
	end := v.fset.Position(n.End())
	startLine := start.Line
	startCol := start.Column
	endLine := end.Line
	endCol := end.Column
	// The blocks are sorted, so we can stop counting as soon as we reach the end of the relevant block.
	for _, b := range v.profile.Blocks {
		if b.StartLine > endLine || (b.StartLine == endLine && b.StartCol >= endCol) {
			// Past the end of the function.
			break
		}
		if b.EndLine < startLine || (b.EndLine == startLine && b.EndCol <= startCol) {
			// Before the beginning of the function
			continue
		}
		for i := b.StartLine; i <= b.EndLine; i++ {
			if !v.ignoreNonCodeLines || v.validLines[i] {
				method.Lines.AddOrUpdateLine(i, int64(b.Count), v.profile.Mode)
			}
		}
	}
	return method
}

// classForFunc returns the Class for a function. For FuncDecl nodes, the
// class is determined by the receiver type (or "-" for package-level funcs).
// For variable functions (nil FuncDecl), the class is "-" (same as
// package-level functions without receivers). When -by-files is set, the
// file path is always used as the class name.
func (v *fileVisitor) classForFunc(n *ast.FuncDecl) *Class {
	var className string
	if v.byFiles {
		// NOTE(boumenot): ReportGenerator creates links that collide if names are not distinct.
		// The work around is to generate a fully qualified name based on the file path.
		className = strings.ReplaceAll(v.fileName, "/", ".")
	} else if n != nil {
		className = v.recvName(n)
	} else {
		className = "-"
	}
	class := v.classes[className]
	if class == nil {
		class = &Class{Name: className, Filename: v.fileName, Methods: []*Method{}, Lines: []*Line{}}
		v.classes[className] = class
		v.pkg.Classes = append(v.pkg.Classes, class)
	}
	return class
}

func (v *fileVisitor) recvName(n *ast.FuncDecl) string {
	if n.Recv == nil {
		return "-"
	}
	recv := n.Recv.List[0].Type
	start := v.fset.Position(recv.Pos())
	end := v.fset.Position(recv.End())
	name := string(v.fileData[start.Offset:end.Offset])
	return strings.TrimSpace(strings.TrimLeft(name, "*"))
}
