package ojsgorm

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

var errValidationTransportReached = errors.New("ojsgorm: validation transport reached")

type validationRoundTripper func(*http.Request) (*http.Response, error)

func (f validationRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var enqueueValidationClient = func() *ojs.Client {
	client, err := ojs.NewClient(
		"http://validation.invalid",
		ojs.WithHTTPClient(&http.Client{
			Transport: validationRoundTripper(func(*http.Request) (*http.Response, error) {
				return nil, errValidationTransportReached
			}),
		}),
		ojs.WithRetryConfig(ojs.RetryConfig{}),
	)
	if err != nil {
		panic(fmt.Sprintf("ojsgorm: creating enqueue validation client: %v", err))
	}
	return client
}()

// validateEnqueueInput delegates job type and queue validation to the SDK
// without performing network I/O. Reaching the sentinel transport means all
// SDK validation completed successfully.
func validateEnqueueInput(jobType string, opts ...ojs.EnqueueOption) error {
	for i, opt := range opts {
		if opt == nil {
			return fmt.Errorf("ojsgorm: enqueue option %d must not be nil", i)
		}
	}

	_, err := enqueueValidationClient.Enqueue(context.Background(), jobType, ojs.Args{}, opts...)
	if errors.Is(err, errValidationTransportReached) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("ojsgorm: enqueue validation unexpectedly reached a successful response")
	}
	return err
}

func validateStoredJob(jobType, queue string) error {
	var opts []ojs.EnqueueOption
	if queue != "" {
		opts = append(opts, ojs.WithQueue(queue))
	}
	return validateEnqueueInput(jobType, opts...)
}
