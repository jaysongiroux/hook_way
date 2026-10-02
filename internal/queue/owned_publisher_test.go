package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type closeRecorder struct {
	Publisher
	closed atomic.Int32
	err    error
}

func TestNativeBrokerConnectorsOwnCleanupOnSuccessAndStartupFailure(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	for _, kind := range []string{"sqs", "sns"} {
		for _, fail := range []bool{false, true} {
			t.Run(kind+"/"+fmt.Sprint(fail), func(t *testing.T) {
				closed := make(chan struct{}, 8)
				srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/x-amz-json-1.0")
					if fail {
						w.Header().Set("X-Amzn-Errortype", "InvalidParameterValue")
						w.WriteHeader(400)
						_, _ = fmt.Fprint(w, `{"message":"synthetic startup failure"}`)
						return
					}
					if kind == "sns" {
						w.Header().Set("Content-Type", "text/xml")
						_, _ = fmt.Fprint(w, `<GetTopicAttributesResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/"><GetTopicAttributesResult><Attributes/></GetTopicAttributesResult><ResponseMetadata><RequestId>synthetic</RequestId></ResponseMetadata></GetTopicAttributesResponse>`)
					} else if r.Header.Get("X-Amz-Target") == "AmazonSQS.GetQueueAttributes" {
						_, _ = fmt.Fprint(w, `{"Attributes":{}}`)
					} else {
						_ = json.NewEncoder(w).Encode(map[string]string{"QueueUrl": "http://" + r.Host + "/synthetic"})
					}
				}))
				srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
					if state == http.StateClosed {
						select {
						case closed <- struct{}{}:
						default:
						}
					}
				}
				srv.Start()
				defer srv.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				factory := NewFactory(FactoryOptions{})
				var pub Publisher
				var err error
				if kind == "sqs" {
					pub, err = factory.sqsConnector(ctx, "sample", SQSConfig{Region: "us-east-1", Endpoint: srv.URL, AccessKeyID: "synthetic", SecretAccessKey: "synthetic", AdmissionBudget: 32, Targets: []SQSTarget{{Name: "synthetic"}}})
				} else {
					pub, err = factory.snsConnector(ctx, "sample", SNSConfig{Region: "us-east-1", Endpoint: srv.URL, AccessKeyID: "synthetic", SecretAccessKey: "synthetic", AdmissionBudget: 32, Targets: []SNSTarget{{Name: "synthetic", ARN: "arn:aws:sns:us-east-1:000000000000:synthetic"}}})
				}
				if fail {
					if err == nil || pub != nil {
						t.Fatal("failed startup returned a live publisher", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if _, ok := pub.(*ownedPublisher); !ok {
						t.Fatal("connector lost ownership")
					}
					if err = pub.Close(); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-closed:
				case <-ctx.Done():
					t.Fatal("native connector left idle connection open", fail)
				}
			})
		}
	}
}

func (p *closeRecorder) Close() error { p.closed.Add(1); return p.err }

func TestOwnedPublisherClosesIdlePoolOnceAfterPublisherEvenOnError(t *testing.T) {
	want := errors.New("close failed")
	base := &closeRecorder{err: want}
	var cleaned atomic.Int32
	p := &ownedPublisher{Publisher: base, closeIdle: func() {
		if base.closed.Load() != 1 {
			t.Error("pool cleanup ran before publisher close")
		}
		cleaned.Add(1)
	}}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !errors.Is(p.Close(), want) {
				t.Error("close error lost")
			}
		}()
	}
	wg.Wait()
	if base.closed.Load() != 1 || cleaned.Load() != 1 {
		t.Fatal("resources closed more than once")
	}
}
