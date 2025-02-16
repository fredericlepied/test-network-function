package utils

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/test-network-function/test-network-function/pkg/tnf"
	"github.com/test-network-function/test-network-function/pkg/tnf/handlers/generic"
	"github.com/test-network-function/test-network-function/pkg/tnf/interactive"
)

var (
	// pathRelativeToRoot is used to calculate relative filepaths to the tnf folder.
	pathRelativeToRoot = path.Join("..")
	// commandHandlerFilePath is the file location of the command handler.
	commandHandlerFilePath = path.Join(pathRelativeToRoot, "pkg", "tnf", "handlers", "command", "command.json")
	// handlerJSONSchemaFilePath is the file location of the json handlers generic schema.
	handlerJSONSchemaFilePath = path.Join(pathRelativeToRoot, "schemas", "generic-test.schema.json")
)

// ArgListToMap takes a list of strings of the form "key=value" and translate it into a map
// of the form {key: value}
func ArgListToMap(lst []string) map[string]string {
	retval := make(map[string]string)
	for _, arg := range lst {
		splitArgs := strings.Split(arg, "=")
		if len(splitArgs) == 1 {
			retval[splitArgs[0]] = ""
		} else {
			retval[splitArgs[0]] = splitArgs[1]
		}
	}
	return retval
}

// FilterArray takes a list and a predicate and returns a list of all elements for whom the predicate returns true
func FilterArray(vs []string, f func(string) bool) []string {
	vsf := make([]string, 0)
	for _, v := range vs {
		if f(v) {
			vsf = append(vsf, v)
		}
	}
	return vsf
}

func CheckFileExists(filePath, name string) {
	fullPath, _ := filepath.Abs(filePath)
	if _, err := os.Stat(fullPath); err == nil {
		log.Infof("Path to %s file found and valid: %s ", name, fullPath)
	} else if errors.Is(err, os.ErrNotExist) {
		log.Fatalf("Path to %s file not found: %s , Exiting", name, fullPath)
	} else {
		log.Fatalf("Path to %s file not valid: %s , err=%s, exiting", name, fullPath, err)
	}
}

// ExecuteCommand uses the generic command handler to execute an arbitrary interactive command, returning
// its output wihout any other check.
func ExecuteCommand(command string, timeout time.Duration, context *interactive.Context, failureCallbackFun func()) string {
	log.Debugf("Executing command: %s", command)

	values := make(map[string]interface{})
	// Escapes the double quote char to make a valid json string.
	values["COMMAND"] = strings.ReplaceAll(command, "\"", "\\\"")
	values["TIMEOUT"] = timeout.Nanoseconds()

	tester, handler, result, err := generic.NewGenericFromMap(commandHandlerFilePath, handlerJSONSchemaFilePath, values)

	gomega.Expect(err).To(gomega.BeNil())
	gomega.Expect(result).ToNot(gomega.BeNil())
	gomega.Expect(result.Valid()).To(gomega.BeTrue())
	gomega.Expect(handler).ToNot(gomega.BeNil())
	gomega.Expect(tester).ToNot(gomega.BeNil())

	test, err := tnf.NewTest(context.GetExpecter(), *tester, handler, context.GetErrorChannel())
	gomega.Expect(err).To(gomega.BeNil())
	gomega.Expect(tester).ToNot(gomega.BeNil())

	test.RunAndValidateWithFailureCallback(failureCallbackFun)

	genericTest := (*tester).(*generic.Generic)
	gomega.Expect(genericTest).ToNot(gomega.BeNil())

	matches := genericTest.Matches
	gomega.Expect(len(matches)).To(gomega.Equal(1))
	match := genericTest.GetMatches()[0]
	return match.Match
}
