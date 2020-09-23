package mariner

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/robertkrimen/otto"

	cwl "github.com/uc-cdis/cwl.go"
)

// this file contains code for loading/processing inputs for *Tools
// for an explanation of the PreProcessContext function, see "explanation for PreProcessContext" comment in js.go

// LoadInputs passes parameter value to input.Provided for each input
// in this setting, "ValueFrom" may appear either in:
//  - tool.Task.Root.Inputs[i].inputBinding.ValueFrom, OR
//  - tool.OriginalStep.In[i].ValueFrom
// need to handle both cases - first eval at the workflowStepInput level, then eval at the tool input level
// if err and input is not optional, it is a fatal error and the run should fail out
func (tool *Tool) loadInputs() (err error) {
	tool.Task.infof("begin load inputs")
	sort.Sort(tool.Task.Root.Inputs)
	tool.buildStepInputMap()
	for _, in := range tool.Task.Root.Inputs {
		if err = tool.loadInput(in); err != nil {
			return tool.Task.errorf("failed to load input: %v", err)
		}
		// map parameter to value for log
		if in.Provided != nil {
			tool.Task.Log.Input[in.ID] = in.Provided.Raw
		} else {
			// implies an unused input parameter
			// e.g., an optional input with no value or default provided
			tool.Task.Log.Input[in.ID] = nil
		}
	}
	tool.Task.infof("end load inputs")
	return nil
}

// used in loadInput() to handle case of workflow step input valueFrom case
func (tool *Tool) buildStepInputMap() {
	// if this tool is not a child task of some parent workflow
	// i.e., if this whole "workflow" consists of just a single tool
	if tool.Task.OriginalStep == nil {
		tool.Task.infof("tool has no parent workflow")
		return
	}

	tool.Task.infof("begin build step input map")
	tool.StepInputMap = make(map[string]*cwl.StepInput)
	// serious "gotcha": https://medium.com/@betable/3-go-gotchas-590b8c014e0a
	/*
		"Go uses a copy of the value instead of the value itself within a range clause.
		So when we take the pointer of value, we’re actually taking the pointer of a copy
		of the value. This copy gets reused throughout the range clause [...]"
	*/
	for j := range tool.Task.OriginalStep.In {
		localID := lastInPath(tool.Task.OriginalStep.In[j].ID)
		tool.StepInputMap[localID] = &tool.Task.OriginalStep.In[j]
	}
	tool.Task.infof("end build step input map")
}

// loadInput passes input parameter value to input.Provided
func (tool *Tool) loadInput(input *cwl.Input) (err error) {
	tool.Task.infof("begin load input: %v", input.ID)

	// debug
	fmt.Printf("------ begin load input: %v -------- \n", input.ID)
	printJSON(input)

	// transformInput() handles any valueFrom statements at the workflowStepInput level and the tool input level
	// to be clear: "workflowStepInput level" refers to this tool and its inputs as they appear as a step in a workflow
	// so that would be specified in a cwl workflow file like Workflow.cwl
	// and the "tool input level" refers to the tool and its inputs as they appear in a standalone tool specification
	// so that information would be specified in a cwl tool file like CommandLineTool.cwl or ExpressionTool.cwl
	fmt.Print(1)
	required := true
	if provided, err := tool.transformInput(input); err == nil {
		fmt.Print(2)
		if provided == nil {
			// optional input with no value or default provided
			// this is an unused input parameter
			// and so does not show up on the command line
			// so here we set the binding to nil to signal to mariner later on
			// -- to not look at this input when building the tool command
			required = false
			input.Binding = nil
		}
		// debug gwas
		// handle case: optional input with no value or default provided
		// -- an input param that goes unused
		// tool.transformInput(input) returns (nil, nil)
		input.Provided = cwl.Provided{}.New(input.ID, provided)
		fmt.Print(3)
	} else {
		fmt.Print(4)
		return tool.Task.errorf("failed to transform input: %v; error: %v", input.ID, err)
	}

	fmt.Print(5)
	if required && input.Default == nil && input.Binding == nil && input.Provided == nil {
		fmt.Print(6)
		printJSON(input)
		return tool.Task.errorf("required input %s value not provided and no default specified", input.ID)
	}
	fmt.Print(7)
	if key, needed := input.Types[0].NeedRequirement(); needed {
		for _, req := range tool.Task.Root.Requirements {
			for _, requiredtype := range req.Types {
				if requiredtype.Name == key {
					input.RequiredType = &requiredtype
					input.Requirements = tool.Task.Root.Requirements
				}
			}
		}
	}
	tool.Task.infof("end load input: %v", input.ID)
	return nil
}

// called in transformInput() routine
// handles path prefix issue
func processFile(f interface{}) (*File, error) {
	path, err := filePath(f)
	if err != nil {
		return nil, err
	}

	// handle the filepath prefix issue
	//
	// Mapping:
	// ---- COMMONS/<guid> -> /commons-data/by-guid/<guid>
	// ---- USER/<path> -> /user-data/<path> // not implemented yet
	// ---- <path> -> <path> // no path processing required, implies file lives in engine workspace
	switch {
	case strings.HasPrefix(path, commonsPrefix):
		/*
			~ Path represenations/handling for commons-data ~

			inputs.json: 				"COMMONS/<guid>"
			mariner: "/commons-data/data/by-guid/<guid>"

			gen3-fuse mounts those objects whose GUIDs appear in the manifest
			gen3-fuse mounts to /commons-data/data/
			and mounts the directories "by-guid/", "by-filepath/", and "by-filename/"

			commons files can be accessed via the full path "/commons-data/data/by-guid/<guid>"

			so the mapping that happens in this path processing step is this:
			"COMMONS/<guid>" -> "/commons-data/data/by-guid/<guid>"
		*/
		GUID := strings.TrimPrefix(path, commonsPrefix)
		path = strings.Join([]string{pathToCommonsData, GUID}, "")
	case strings.HasPrefix(path, userPrefix):
		/*
			~ Path representations/handling for user-data ~

			s3: 			   "/userID/path/to/file"
			inputs.json: 	      "USER/path/to/file"
			mariner: 		"/engine-workspace/path/to/file"

			user-data bucket gets mounted at the 'userID' prefix to dir /engine-workspace/

			so the mapping that happens in this path processing step is this:
			"USER/path/to/file" -> "/engine-workspace/path/to/file"
		*/
		trimmedPath := strings.TrimPrefix(path, userPrefix)
		path = strings.Join([]string{"/", engineWorkspaceVolumeName, "/", trimmedPath}, "")
	case strings.HasPrefix(path, conformancePrefix):
		trimmedPath := strings.TrimPrefix(path, conformancePrefix)
		path = strings.Join([]string{"/", conformanceVolumeName, "/", trimmedPath}, "")
	}
	return fileObject(path), nil
}

// called in transformInput() routine
func processFileList(l interface{}) ([]*File, error) {
	if reflect.TypeOf(l).Kind() != reflect.Array {
		return nil, fmt.Errorf("not an array")
	}

	var err error
	var f *File
	var i interface{}
	out := []*File{}
	s := reflect.ValueOf(l)
	for j := 0; j < s.Len(); j++ {
		i = s.Index(j)
		if !isFile(i) {
			return nil, fmt.Errorf("nonFile object found in file array: %v", i)
		}
		if f, err = processFile(i); err != nil {
			return nil, fmt.Errorf("failed to process file %v", i)
		}
		out = append(out, f)
	}
	return out, nil
}

// if err and input is not optional, it is a fatal error and the run should fail out
func (tool *Tool) transformInput(input *cwl.Input) (out interface{}, err error) {
	tool.Task.infof("begin transform input: %v", input.ID)

	// debug
	fmt.Printf("begin transform input: %v\n", input.ID)

	/*
		NOTE: presently only context loaded into js vm's here is `self`
		Will certainly need to add more context to handle all cases
		Definitely, definitely need a generalized method for loading appropriate context at appropriate places
		In particular, the `inputs` context is probably going to be needed most commonly

		OTHERNOTE: `self` (in js vm) takes on different values in different places, according to cwl docs
		see: https://www.commonwl.org/v1.0/Workflow.html#Parameter_references
		---
		Steps:
		1. handle ValueFrom case at stepInput level
		 - if no ValueFrom specified, assign parameter value to `out` to processed in next step
		2. handle ValueFrom case at toolInput level
		 - initial value is `out` from step 1
	*/
	localID := lastInPath(input.ID)

	fmt.Println(1)
	// stepInput ValueFrom case
	if tool.StepInputMap[localID] != nil {
		fmt.Println(2)
		fmt.Println("step input map:")
		printJSON(tool.StepInputMap)
		// no processing needs to happen if the valueFrom field is empty
		if tool.StepInputMap[localID].ValueFrom != "" {
			fmt.Println(3)

			// here the valueFrom field is not empty, so we need to handle valueFrom
			valueFrom := tool.StepInputMap[localID].ValueFrom
			fmt.Println(4)
			if strings.HasPrefix(valueFrom, "$") {
				// valueFrom is an expression that needs to be eval'd

				// get a js vm
				vm := otto.New()

				// preprocess struct/array so that fields can be accessed in vm
				// Question: how to handle non-array/struct data types?
				// --------- no preprocessing should have to happen in this case.
				self, err := tool.loadInputValue(input)
				if err != nil {
					return nil, tool.Task.errorf("failed to load value: %v", err)
				}
				self, err = preProcessContext(self)
				if err != nil {
					return nil, tool.Task.errorf("failed to preprocess context: %v", err)
				}

				// set `self` variable in vm
				if err = vm.Set("self", self); err != nil {
					return nil, tool.Task.errorf("failed to set 'self' value in js vm: %v", err)
				}

				/*
					// Troubleshooting js
					// note: when accessing object fields using keys must use otto.Run("obj.key"), NOT otto.Get("obj.key")

					fmt.Println("self in js:")
					jsSelf, err := vm.Get("self")
					jsSelfVal, err := jsSelf.Export()
					PrintJSON(jsSelfVal)

					fmt.Println("Expression:")
					PrintJSON(valueFrom)

					fmt.Println("Object.keys(self)")
					keys, err := vm.Run("Object.keys(self)")
					if err != nil {
						fmt.Printf("Error evaluating Object.keys(self): %v\n", err)
					}
					keysVal, err := keys.Export()
					PrintJSON(keysVal)
				*/

				//  eval the expression in the vm, capture result in `out`
				if out, err = evalExpression(valueFrom, vm); err != nil {
					return nil, tool.Task.errorf("failed to eval js expression: %v; error: %v", valueFrom, err)
				}
			} else {
				// valueFrom is not an expression - take raw string/val as value
				out = valueFrom
			}
		}
	}

	// if this tool is not a step of a parent workflow
	// OR
	// if this tool is a step of a parent workflow but the valueFrom is empty
	if out == nil {
		out, err = tool.loadInputValue(input)
		if err != nil {
			return nil, tool.Task.errorf("failed to load input value: %v", err)
		}
		if out == nil {
			// implies an optional parameter with no value provided and no default value specified
			// this input parameter is not used by the tool
			tool.Task.infof("optional input with no value or default provided - skipping: %v", input.ID)
			return nil, nil
		}
	}

	// fmt.Println("before creating file object:")
	// PrintJSON(out)

	// if file, need to ensure that all file attributes get populated (e.g., basename)
	/*
		fixme: handle array of files
		Q: what about directories (?)

		do this:

		switch statement:
		case file
		case []file

		note: check types in the param type list?
		vs. checking types of actual values
	*/
	switch {
	case isFile(out):
		if out, err = processFile(out); err != nil {
			return nil, tool.Task.errorf("failed to process file: %v; error: %v", out, err)
		}
	case isArrayOfFile(out):
		if out, err = processFileList(out); err != nil {
			return nil, tool.Task.errorf("failed to process file list: %v; error: %v", out, err)
		}
	default:
		// fmt.Println("is not a file object")
	}

	// fmt.Println("after creating file object:")
	// PrintJSON(out)

	// at this point, variable `out` is the transformed input thus far (even if no transformation actually occured)
	// so `out` will be what we work with in this next block as an initial value
	// tool inputBinding ValueFrom case
	if input.Binding != nil && input.Binding.ValueFrom != nil {
		valueFrom := input.Binding.ValueFrom.String
		// fmt.Println("here is valueFrom:")
		// fmt.Println(valueFrom)
		if strings.HasPrefix(valueFrom, "$") {
			vm := otto.New()
			var context interface{}
			// fixme: handle array of files
			switch out.(type) {
			case *File, []*File:
				// fmt.Println("context is a file or array of files")
				context, err = preProcessContext(out)
				if err != nil {
					return nil, tool.Task.errorf("failed to preprocess context: %v", err)
				}
			default:
				// fmt.Println("context is not a file")
				context = out
			}

			vm.Set("self", context) // NOTE: again, will more than likely need additional context here to cover other cases
			if out, err = evalExpression(valueFrom, vm); err != nil {
				return nil, tool.Task.errorf("failed to eval expression: %v; error: %v", valueFrom, err)
			}
		} else {
			// not an expression, so no eval necessary - take raw value
			out = valueFrom
		}
	}

	// fmt.Println("Here's tranformed input:")
	// PrintJSON(out)
	tool.Task.infof("end transform input: %v", input.ID)
	return out, nil
}

/*
loadInputValue logic:
1. take value from params
2. if not given in params:
	i) take default value
	ii) if no default provided:
		a) if optional param, return nil, nil
		b) if required param, return nil, err (fatal error)
*/

// handles all cases of input params
// i.e., handles all optional/null/default param/value logic
func (tool *Tool) loadInputValue(input *cwl.Input) (out interface{}, err error) {
	tool.Task.infof("begin load input value for input: %v", input.ID)
	var required, ok bool
	// take value from given param value set
	out, ok = tool.Task.Parameters[input.ID]
	// if no value exists in the provided parameter map
	if !ok || out == nil {
		// check if default value is provided
		if input.Default == nil {
			// implies no default value provided

			// determine if this param is required or optional
			required = true
			for _, t := range input.Types {
				if t.Type == CWLNullType {
					required = false
				}
			}

			// return error if this is a required param
			if required {
				return nil, tool.Task.errorf("missing value for required input param %v", input.ID)
			}
		} else {
			// implies a default value is provided
			// in this case you return the default value
			out = input.Default.Self
		}
	}
	tool.Task.infof("end load input value for input: %v", input.ID)
	return out, nil
}

// inputsToVM loads tool.Task.Root.InputsVM with inputs context - using Input.Provided for each input
// to allow js expressions to be evaluated
func (tool *Tool) inputsToVM() (err error) {
	tool.Task.infof("begin load inputs to js vm")
	prefix := tool.Task.Root.ID + "/" // need to trim this from all the input.ID's
	tool.Task.Root.InputsVM = otto.New()
	context := make(map[string]interface{})
	var f interface{}
	for _, input := range tool.Task.Root.Inputs {
		/*
			fmt.Println("input:")
			PrintJSON(input)
			fmt.Println("input provided:")
			PrintJSON(input.Provided)
		*/
		inputID := strings.TrimPrefix(input.ID, prefix)

		// fixme: handle array of files
		// note: this code block is extraordinarily janky and needs to be refactored
		// error here.
		switch {
		case input.Provided == nil:
			context[inputID] = nil
		case input.Types[0].Type == CWLFileType:
			if input.Provided.Entry != nil {
				// no valueFrom specified in inputBinding
				if input.Provided.Entry.Location != "" {
					f = fileObject(input.Provided.Entry.Location)
				}
			} else {
				// valueFrom specified in inputBinding - resulting value stored in input.Provided.Raw
				switch input.Provided.Raw.(type) {
				case string:
					f = fileObject(input.Provided.Raw.(string))
				case *File, []*File:
					f = input.Provided.Raw
				default:
					return tool.Task.errorf("unexpected datatype representing file object in input.Provided.Raw")
				}
			}
			fileContext, err := preProcessContext(f)
			if err != nil {
				return tool.Task.errorf("failed to preprocess file context: %v; error: %v", f, err)
			}
			context[inputID] = fileContext
		default:
			context[inputID] = input.Provided.Raw // not sure if this will work in general - so far, so good though - need to test further
		}
	}
	if err = tool.Task.Root.InputsVM.Set("inputs", context); err != nil {
		return tool.Task.errorf("failed to set inputs context in js vm: %v", err)
	}
	tool.Task.infof("end load inputs to js vm")
	return nil
}
