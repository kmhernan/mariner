package mariner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/robertkrimen/otto"
)

// this file contains code for evaluating JS expressions encountered in the CWL
// EvalExpression evals a single expression (something like $(...) or ${...})
// resolveExpressions processes a string which may contain several embedded expressions, each wrapped in their own $()/${} wrapper

// NOTE: make uniform either UpperCase, or camelCase for naming functions
// ----- none of these names really need to be exported, since they get called within the `mariner` package

// getJS strips the cwl prefix for an expression
// and tells whether to just eval the expression, or eval the exp as a js function
// this is modified from the cwl.Eval.ToJavaScriptString() method
func js(s string) (js string, fn bool, err error) {
	// if curly braces, then need to eval as a js function
	// see https://www.commonwl.org/v1.0/Workflow.html#Expressions
	fn = strings.HasPrefix(s, "${")
	s = strings.TrimLeft(s, "$(\n")
	s = strings.TrimRight(s, ")\n")
	return s, fn, nil
}

// EvalExpression is an engine for handling in-line js in cwl
// the exp is passed before being stripped of any $(...) or ${...} wrapper
// the vm must be loaded with all necessary context for eval
// EvalExpression handles parameter references and expressions $(...), as well as functions ${...}
func evalExpression(exp string, vm *otto.Otto) (result interface{}, err error) {
	// strip the $() (or if ${} just trim leading $), which appears in the cwl as a wrapper for js expressions
	var output otto.Value
	js, fn, _ := js(exp)
	if js == "" {
		return nil, fmt.Errorf("empty expression")
	}
	if fn {
		// if expression wrapped like ${...}, need to run as a zero arg js function

		// construct js function definition
		fnDef := fmt.Sprintf("function f() %s", js)
		// fmt.Printf("Here's the fnDef:\n%v\n", fnDef)

		// run this function definition so the function exists in the vm
		vm.Run(fnDef)

		// call this function in the vm
		output, err = vm.Run("f()")
		if err != nil {
			fmt.Printf("\terror running js function: %v\n", err)
			return nil, err
		}
	} else {
		output, err = vm.Run(js)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate js expression: %v", err)
		}
	}
	result, _ = output.Export()
	return result, nil
}

func (tool *Tool) evalExpression(exp string) (result interface{}, err error) {
	return evalExpression(exp, tool.Task.Root.InputsVM)
}

// resolveExpressions processes a text field which may or may not be
// - one expression
// - a string literal
// - a string which contains one or more separate JS expressions, each wrapped like $(...)
// presently writing simple case to return a string only for use in the argument valueFrom case
// can easily extend in the future to be used for any field, to return any kind of value
// NOTE: should work - needs to be tested more
// algorithm works in goplayground: https://play.golang.org/p/YOv-K-qdL18
func (tool *Tool) resolveExpressions(inText string) (outText string, err error) {
	r := bufio.NewReader(strings.NewReader(inText))
	var c0, c1, c2 string
	var done bool
	image := make([]string, 0)
	for !done {
		nextRune, _, err := r.ReadRune()
		if err != nil {
			if err == io.EOF {
				done = true
			} else {
				return "", err
			}
		}
		c0, c1, c2 = c1, c2, string(nextRune)
		if c1 == "$" && c2 == "(" && c0 != "\\" {
			// indicates beginning of expression block

			// read through to the end of this expression block
			expression, err := r.ReadString(')')
			if err != nil {
				return "", err
			}

			// get full $(...) expression
			expression = c1 + c2 + expression

			// eval that thing
			result, err := evalExpression(expression, tool.Task.Root.InputsVM)
			if err != nil {
				return "", err
			}

			// result ought to be a string
			val, ok := result.(string)
			if !ok {
				return "", fmt.Errorf("js embedded in string did not return a string")
			}

			// cut off trailing "$" that had already been collected
			image = image[:len(image)-1]

			// collect resulting string
			image = append(image, val)
		} else {
			if !done {
				// checking done so as to not collect null value
				image = append(image, string(c2))
			}
		}
	}
	// get resolved string value
	outText = strings.Join(image, "")
	return outText, nil
}

/*
	explanation for PreProcessContext

	problem: setting variable name in a js vm to a golang struct doesn't work

	suggested solution by otto examples/docs: use otto.ToValue()

	NOTE: otto library does NOT handle case of converting "complex data types" (e.g., structs) to js objects
	see `func (self *_runtime) toValue(value interface{})` in `runtime.go` in otto library
	comment from otto developer in the typeSwitch in the *_runtime.toValue() method:
	"
	case Object, *Object, _object, *_object:
		// Nothing happens.
		// FIXME We should really figure out what can come here.
		// This catch-all is ugly.
	"

	that means we need to preprocess structs (to a map or array of maps (?)) before loading to vm

	real solution: marshal any struct into json, and then unmarshal that json into a map[string]interface{}
	set the variable in the vm to this map
	this works, and is a simple, elegant solution
	way better to do this json marshal/unmarshal than to handle individual cases
	could suggest this to the otto developer to fix his object handling dilemma
*/

// PreProcessContext is a utility function to preprocess any struct/array before loading into js vm (see above note)
// NOTE: using this json marshalling/unmarshalling strategy implies that the struct field names
// ----- get loaded into the js vm as their json representation/alias.
// ----- this means we can use the cwl fields as json aliases for any struct type's fields
// ----- and then using this function to preprocess the struct/array, all the keys/data will get loaded in properly
// ----- which saves us from having to handle special cases
func preProcessContext(in interface{}) (out interface{}, err error) {
	j, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(j, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}
