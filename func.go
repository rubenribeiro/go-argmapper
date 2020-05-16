package argmapper

import (
	"fmt"
	"reflect"
	"unicode"
	"unicode/utf8"

	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/go-argmapper/internal/graph"
)

type Func struct {
	fn  reflect.Value
	arg *structType
}

func NewFunc(f interface{}) (*Func, error) {
	fv := reflect.ValueOf(f)
	ft := fv.Type()
	if k := ft.Kind(); k != reflect.Func {
		return nil, fmt.Errorf("fn should be a function, got %s", k)
	}

	// We only accept zero or 1 arguments right now. In the future we
	// could potentially expand this to support multiple args that are
	// all structs we populate but for now lets just simplify this.
	if ft.NumIn() > 1 {
		return nil, fmt.Errorf("function must take one struct arg")
	}

	// Our argument must be a struct
	typ := ft.In(0)
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("function must take one struct arg")
	}

	structTyp, err := newStructType(typ)
	if err != nil {
		return nil, err
	}

	return &Func{
		fn:  fv,
		arg: structTyp,
	}, nil
}

func (f *Func) Call(opts ...Arg) Result {
	// Build up our args
	builder := &argBuilder{
		named: make(map[string]reflect.Value),
	}
	for _, opt := range opts {
		opt(builder)
	}

	// Start building our graph. The first step is to add our own vertex.
	// Then we go through all the named inputs we have and add them to the
	// graph, with an edge from our function to the inputs we require.
	var g graph.Graph
	vertex := g.Add(funcVertex{
		Func: f,
	})
	for k, f := range f.arg.fields {
		g.AddEdge(g.Add(valueVertex{
			Name: k,
			Type: f.Type,
		}), vertex)
	}

	// Values is the built up list of values we know about
	vertexValues := map[graph.Vertex]reflect.Value{}
	inValues := map[string]reflect.Value{}

	// Next, we add the values that we know about. These may overlap with
	// inputs we require but the graph ensures that the same vertices are
	// added only once.
	inputs := map[interface{}]struct{}{}
	for k, v := range builder.named {
		input := g.Add(valueVertex{
			Name: k,
			Type: v.Type(),
		})

		inputs[graph.VertexID(input)] = struct{}{}
		vertexValues[input] = v
	}

	// Find all the paths to our function
	visited := map[interface{}]struct{}{}
	g.Reverse().DFS(vertex, func(v graph.Vertex, next func() error) error {
		// Mark this as visited
		visited[graph.VertexID(v)] = struct{}{}

		// If we arrived at an input we have, then we don't go deeper.
		if _, ok := inputs[graph.VertexID(v)]; ok {
			return nil
		}

		return next()
	})

	// Let's walk the graph and print out our paths
	println(fmt.Sprintf("%s", g.KahnSort()))
	for _, current := range g.KahnSort() {
		// If we arrived at our function, we've satisfied our inputs.
		if current == vertex {
			break
		}

		// Depending on the type of vertex, we execute
		switch v := current.(type) {
		case valueVertex:
			// We have a value.
			if val, ok := vertexValues[v]; ok {
				inValues[v.Name] = val
			}
		}
	}

	// Initialize the struct we'll be populating
	var buildErr error
	structVal := f.arg.New()
	for k, _ := range f.arg.fields {
		v, ok := inValues[k]
		if !ok {
			buildErr = multierror.Append(buildErr, fmt.Errorf(
				"argument cannot be satisfied: %s", k))
			continue
		}

		structVal.Field(k).Set(v)
	}

	if buildErr != nil {
		return Result{buildErr: buildErr}
	}

	// Call our function
	out := f.fn.Call([]reflect.Value{structVal.Value()})

	return Result{out: out}
}

func firstToLower(s string) string {
	if len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r != utf8.RuneError || size > 1 {
			lo := unicode.ToLower(r)
			if lo != r {
				s = string(lo) + s[size:]
			}
		}
	}
	return s
}

// errType is used for comparison in Spec
var errType = reflect.TypeOf((*error)(nil)).Elem()
