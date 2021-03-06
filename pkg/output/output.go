package output

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/pkg/errors"
)

const (
	PhaseOpString = "###Phase-output###:"
)

type Output struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func marshalOutput(key, value string) (string, error) {
	out := &Output{
		Key:   key,
		Value: value,
	}
	outString, err := json.Marshal(out)
	if err != nil {
		return "", errors.Wrap(err, "Failed to marshal key-value pair")
	}
	return string(outString), nil
}

// UnmarshalOutput unmarshals output json into Output struct
func UnmarshalOutput(opString string) (*Output, error) {
	p := &Output{}
	err := json.Unmarshal([]byte(opString), p)
	return p, errors.Wrap(err, "Failed to unmarshal key-value pair")
}

// ValidateKey validates the key argument
func ValidateKey(key string) error {
	// key should be non-empty
	if key == "" {
		return errors.New("Key should not be empty")
	}
	// key can contain only alpha numeric characters and underscore
	valid := regexp.MustCompile("^[a-zA-Z0-9_]*$").MatchString
	if !valid(key) {
		return errors.New("Key should contain only alphanumeric characters and underscore")
	}
	return nil
}

// PrintOutput runs the `kando output` command
func PrintOutput(key, value string) error {
	outString, err := marshalOutput(key, value)
	if err != nil {
		return err
	}
	fmt.Println(PhaseOpString, outString)
	return nil
}
