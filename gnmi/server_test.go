/* Copyright 2017 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gnmi

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/openconfig/gnmi/coalesce"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	pb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/google/gnxi/gnmi/modeldata"
	"github.com/google/gnxi/gnmi/modeldata/gostruct"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var (
	// model is the model for test config server.
	model = &Model{
		modelData:       modeldata.ModelData,
		structRootType:  reflect.TypeOf((*gostruct.Device)(nil)),
		schemaTreeRoot:  gostruct.SchemaTree["Device"],
		jsonUnmarshaler: gostruct.Unmarshal,
		enumData:        gostruct.ΛEnum,
	}
)

func TestCapabilities(t *testing.T) {
	s, err := NewServer(model, nil, nil)
	if err != nil {
		t.Fatalf("error in creating server: %v", err)
	}
	resp, err := s.Capabilities(nil, &pb.CapabilityRequest{})
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if !reflect.DeepEqual(resp.GetSupportedModels(), model.modelData) {
		t.Errorf("got supported models %v\nare not the same as\nmodel supported by the server %v", resp.GetSupportedModels(), model.modelData)
	}
	if !reflect.DeepEqual(resp.GetSupportedEncodings(), supportedEncodings) {
		t.Errorf("got supported encodings %v\nare not the same as\nencodings supported by the server %v", resp.GetSupportedEncodings(), supportedEncodings)
	}
}

func TestGet(t *testing.T) {
	jsonConfigRoot := `{
		"openconfig-system:system": {
			"openconfig-openflow:openflow": {
				"agent": {
					"config": {
						"failure-mode": "SECURE",
						"max-backoff": 10
					}
				}
			}
		},
	  "openconfig-platform:components": {
	    "component": [
	      {
	        "config": {
	          "name": "swpri1-1-1"
	        },
	        "name": "swpri1-1-1"
	      }
	    ]
	  }
	}`

	s, err := NewServer(model, []byte(jsonConfigRoot), nil)
	if err != nil {
		t.Fatalf("error in creating server: %v", err)
	}

	tds := []struct {
		desc        string
		textPbPath  string
		modelData   []*pb.ModelData
		wantRetCode codes.Code
		wantRespVal interface{}
	}{{
		desc: "get valid but non-existing node",
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
		`,
		wantRetCode: codes.NotFound,
	}, {
		desc:        "root node",
		wantRetCode: codes.OK,
		wantRespVal: jsonConfigRoot,
	}, {
		desc: "get non-enum type",
		textPbPath: `
					elem: <name: "system" >
					elem: <name: "openflow" >
					elem: <name: "agent" >
					elem: <name: "config" >
					elem: <name: "max-backoff" >
				`,
		wantRetCode: codes.OK,
		wantRespVal: uint64(10),
	}, {
		desc: "get enum type",
		textPbPath: `
					elem: <name: "system" >
					elem: <name: "openflow" >
					elem: <name: "agent" >
					elem: <name: "config" >
					elem: <name: "failure-mode" >
				`,
		wantRetCode: codes.OK,
		wantRespVal: "SECURE",
	}, {
		desc:        "root child node",
		textPbPath:  `elem: <name: "components" >`,
		wantRetCode: codes.OK,
		wantRespVal: `{
							"openconfig-platform:component": [{
								"config": {
						        	"name": "swpri1-1-1"
								},
						        "name": "swpri1-1-1"
							}]}`,
	}, {
		desc: "node with attribute",
		textPbPath: `
								elem: <name: "components" >
								elem: <
									name: "component"
									key: <key: "name" value: "swpri1-1-1" >
								>`,
		wantRetCode: codes.OK,
		wantRespVal: `{
								"openconfig-platform:config": {"name": "swpri1-1-1"},
								"openconfig-platform:name": "swpri1-1-1"
							}`,
	}, {
		desc: "node with attribute in its parent",
		textPbPath: `
								elem: <name: "components" >
								elem: <
									name: "component"
									key: <key: "name" value: "swpri1-1-1" >
								>
								elem: <name: "config" >`,
		wantRetCode: codes.OK,
		wantRespVal: `{"openconfig-platform:name": "swpri1-1-1"}`,
	}, {
		desc: "ref leaf node",
		textPbPath: `
								elem: <name: "components" >
								elem: <
									name: "component"
									key: <key: "name" value: "swpri1-1-1" >
								>
								elem: <name: "name" >`,
		wantRetCode: codes.OK,
		wantRespVal: "swpri1-1-1",
	}, {
		desc: "regular leaf node",
		textPbPath: `
								elem: <name: "components" >
								elem: <
									name: "component"
									key: <key: "name" value: "swpri1-1-1" >
								>
								elem: <name: "config" >
								elem: <name: "name" >`,
		wantRetCode: codes.OK,
		wantRespVal: "swpri1-1-1",
	}, {
		desc: "non-existing node: wrong path name",
		textPbPath: `
								elem: <name: "components" >
								elem: <
									name: "component"
									key: <key: "foo" value: "swpri1-1-1" >
								>
								elem: <name: "bar" >`,
		wantRetCode: codes.NotFound,
	}, {
		desc: "non-existing node: wrong path attribute",
		textPbPath: `
								elem: <name: "components" >
								elem: <
									name: "component"
									key: <key: "foo" value: "swpri2-2-2" >
								>
								elem: <name: "name" >`,
		wantRetCode: codes.NotFound,
	}, {
		desc:        "use of model data not supported",
		modelData:   []*pb.ModelData{{}},
		wantRetCode: codes.Unimplemented,
	}}

	for _, td := range tds {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, s, td.textPbPath, td.wantRetCode, td.wantRespVal, td.modelData)
		})
	}
}

// runTestGet requests a path from the server by Get grpc call, and compares if
// the return code and response value are expected.
func runTestGet(t *testing.T, s *Server, textPbPath string, wantRetCode codes.Code, wantRespVal interface{}, useModels []*pb.ModelData) {
	// Send request
	var pbPath pb.Path
	if err := proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatalf("error in unmarshaling path: %v", err)
	}
	req := &pb.GetRequest{
		Path:      []*pb.Path{&pbPath},
		Encoding:  pb.Encoding_JSON_IETF,
		UseModels: useModels,
	}
	resp, err := s.Get(nil, req)

	// Check return code
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	if gotRetStatus.Code() != wantRetCode {
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	}

	// Check response value
	var gotVal interface{}
	if resp != nil {
		notifs := resp.GetNotification()
		if len(notifs) != 1 {
			t.Fatalf("got %d notifications, want 1", len(notifs))
		}
		updates := notifs[0].GetUpdate()
		if len(updates) != 1 {
			t.Fatalf("got %d updates in the notification, want 1", len(updates))
		}
		val := updates[0].GetVal()
		if val.GetJsonIetfVal() == nil {
			gotVal, err = value.ToScalar(val)
			if err != nil {
				t.Errorf("got: %v, want a scalar value", gotVal)
			}
		} else {
			// Unmarshal json data to gotVal container for comparison
			if err := json.Unmarshal(val.GetJsonIetfVal(), &gotVal); err != nil {
				t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
			}
			var wantJSONStruct interface{}
			if err := json.Unmarshal([]byte(wantRespVal.(string)), &wantJSONStruct); err != nil {
				t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
			}
			wantRespVal = wantJSONStruct
		}
	}

	if !reflect.DeepEqual(gotVal, wantRespVal) {
		t.Errorf("got: %v (%T),\nwant %v (%T)", gotVal, gotVal, wantRespVal, wantRespVal)
	}
}

type gnmiSetTestCase struct {
	desc        string                    // description of test case.
	initConfig  string                    // config before the operation.
	op          pb.UpdateResult_Operation // operation type.
	textPbPath  string                    // text format of gnmi Path proto.
	val         *pb.TypedValue            // value for UPDATE/REPLACE operations. always nil for DELETE.
	wantRetCode codes.Code                // grpc return code.
	wantConfig  string                    // config after the operation.
}

func TestDelete(t *testing.T) {
	tests := []gnmiSetTestCase{{
		desc: "delete leaf node",
		initConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a",
					"login-banner": "Hello!"
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "config" >
			elem: <name: "login-banner" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
	}, {
		desc: "delete sub-tree",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				},
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
	}, {
		desc: "delete a sub-tree with only one leaf node",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				},
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
			elem: <name: "config" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
	}, {
		desc: "delete a leaf node whose parent has only this child",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				},
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
			elem: <name: "config" >
			elem: <name: "timezone-name" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
	}, {
		desc: "delete root",
		initConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
		op:          pb.UpdateResult_DELETE,
		wantRetCode: codes.OK,
		wantConfig:  `{}`,
	}, {
		desc: "delete non-existing node",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
			elem: <name: "config" >
			elem: <name: "foo-bar" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
	}, {
		desc: "delete node with non-existing precedent path",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
			elem: <name: "foo-bar" >
			elem: <name: "timezone-name" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
	}, {
		desc: "delete node with non-existing attribute in precedent path",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
			elem: <
				name: "config"
				key: <key: "name" value: "foo" >
			>
			elem: <name: "timezone-name" >`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
	}, {
		desc: "delete node with non-existing attribute",
		initConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
			elem: <name: "config" >
			elem: <
				name: "timezone-name"
				key: <key: "name" value: "foo" >
			>
			elem: <name: "timezone-name" >`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "Europe/Stockholm"
					}
				}
			}
		}`,
	}, {
		desc: "delete leaf node with attribute in its precedent path",
		initConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						},
						"state": {
							"name": "swpri1-1-1",
							"mfg-name": "foo bar inc."
						}
					}
				]
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "components" >
			elem: <
				name: "component"
				key: <key: "name" value: "swpri1-1-1" >
			>
			elem: <name: "state" >
			elem: <name: "mfg-name" >`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						},
						"state": {
							"name": "swpri1-1-1"
						}
					}
				]
			}
		}`,
	}, {
		desc: "delete sub-tree with attribute in its precedent path",
		initConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						},
						"state": {
							"name": "swpri1-1-1",
							"mfg-name": "foo bar inc."
						}
					}
				]
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "components" >
			elem: <
				name: "component"
				key: <key: "name" value: "swpri1-1-1" >
			>
			elem: <name: "state" >`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						}
					}
				]
			}
		}`,
	}, {
		desc: "delete path node with attribute",
		initConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						}
					},
					{
						"name": "swpri1-1-2",
						"config": {
							"name": "swpri1-1-2"
						}
					}
				]
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "components" >
			elem: <
				name: "component"
				key: <key: "name" value: "swpri1-1-1" >
			>`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-2",
						"config": {
							"name": "swpri1-1-2"
						}
					}
				]
			}
		}`,
	}, {
		desc: "delete path node with int type attribute",
		initConfig: `{
			"system": {
				"openflow": {
					"controllers": {
						"controller": [
							{
								"config": {
									"name": "main"
								},
								"connections": {
									"connection": [
										{
											"aux-id": 0,
											"config": {
												"address": "192.0.2.10",
												"aux-id": 0
											}
										}
									]
								},
								"name": "main"
							}
						]
					}
				}
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "openflow" >
			elem: <name: "controllers" >
			elem: <
				name: "controller"
				key: <key: "name" value: "main" >
			>
			elem: <name: "connections" >
			elem: <
				name: "connection"
				key: <key: "aux-id" value: "0" >
			>
			`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"openflow": {
					"controllers": {
						"controller": [
							{
								"config": {
									"name": "main"
								},
								"name": "main"
							}
						]
					}
				}
			}
		}`,
	}, {
		desc: "delete leaf node with non-existing attribute value",
		initConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						}
					}
				]
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "components" >
			elem: <
				name: "component"
				key: <key: "name" value: "foo" >
			>`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						}
					}
				]
			}
		}`,
	}, {
		desc: "delete leaf node with non-existing attribute value in precedent path",
		initConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						},
						"state": {
							"name": "swpri1-1-1",
							"mfg-name": "foo bar inc."
						}
					}
				]
			}
		}`,
		op: pb.UpdateResult_DELETE,
		textPbPath: `
			elem: <name: "components" >
			elem: <
				name: "component"
				key: <key: "name" value: "foo" >
			>
			elem: <name: "state" >
			elem: <name: "mfg-name" >
		`,
		wantRetCode: codes.OK,
		wantConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						},
						"state": {
							"name": "swpri1-1-1",
							"mfg-name": "foo bar inc."
						}
					}
				]
			}
		}`,
	}}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			runTestSet(t, model, tc)
		})
	}
}

func TestReplace(t *testing.T) {
	systemConfig := `{
		"system": {
			"clock": {
				"config": {
					"timezone-name": "Europe/Stockholm"
				}
			},
			"config": {
				"hostname": "switch_a",
				"login-banner": "Hello!"
			}
		}
	}`

	tests := []gnmiSetTestCase{{
		desc:       "replace root",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{
				JsonIetfVal: []byte(systemConfig),
			}},
		wantRetCode: codes.OK,
		wantConfig:  systemConfig,
	}, {
		desc:       "replace a subtree",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "clock" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{
				JsonIetfVal: []byte(`{"config": {"timezone-name": "US/New York"}}`),
			},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"clock": {
					"config": {
						"timezone-name": "US/New York"
					}
				}
			}
		}`,
	}, {
		desc:       "replace a keyed list subtree",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "components" >
			elem: <
				name: "component"
				key: <key: "name" value: "swpri1-1-1" >
			>`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{
				JsonIetfVal: []byte(`{"config": {"name": "swpri1-1-1"}}`),
			},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"components": {
				"component": [
					{
						"name": "swpri1-1-1",
						"config": {
							"name": "swpri1-1-1"
						}
					}
				]
			}
		}`,
	}, {
		desc: "replace node with int type attribute in its precedent path",
		initConfig: `{
			"system": {
				"openflow": {
					"controllers": {
						"controller": [
							{
								"config": {
									"name": "main"
								},
								"name": "main"
							}
						]
					}
				}
			}
		}`,
		op: pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "openflow" >
			elem: <name: "controllers" >
			elem: <
				name: "controller"
				key: <key: "name" value: "main" >
			>
			elem: <name: "connections" >
			elem: <
				name: "connection"
				key: <key: "aux-id" value: "0" >
			>
			elem: <name: "config" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{
				JsonIetfVal: []byte(`{"address": "192.0.2.10", "aux-id": 0}`),
			},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"openflow": {
					"controllers": {
						"controller": [
							{
								"config": {
									"name": "main"
								},
								"connections": {
									"connection": [
										{
											"aux-id": 0,
											"config": {
												"address": "192.0.2.10",
												"aux-id": 0
											}
										}
									]
								},
								"name": "main"
							}
						]
					}
				}
			}
		}`,
	}, {
		desc:       "replace a leaf node of int type",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "openflow" >
			elem: <name: "agent" >
			elem: <name: "config" >
			elem: <name: "backoff-interval" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_IntVal{IntVal: 5},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"openflow": {
					"agent": {
						"config": {
							"backoff-interval": 5
						}
					}
				}
			}
		}`,
	}, {
		desc:       "replace a leaf node of string type",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "openflow" >
			elem: <name: "agent" >
			elem: <name: "config" >
			elem: <name: "datapath-id" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_StringVal{StringVal: "00:16:3e:00:00:00:00:00"},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"openflow": {
					"agent": {
						"config": {
							"datapath-id": "00:16:3e:00:00:00:00:00"
						}
					}
				}
			}
		}`,
	}, {
		desc:       "replace a leaf node of enum type",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "openflow" >
			elem: <name: "agent" >
			elem: <name: "config" >
			elem: <name: "failure-mode" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_StringVal{StringVal: "SECURE"},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"openflow": {
					"agent": {
						"config": {
							"failure-mode": "SECURE"
						}
					}
				}
			}
		}`,
	}, {
		desc:       "replace an non-existing leaf node",
		initConfig: `{}`,
		op:         pb.UpdateResult_REPLACE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "openflow" >
			elem: <name: "agent" >
			elem: <name: "config" >
			elem: <name: "foo-bar" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_StringVal{StringVal: "SECURE"},
		},
		wantRetCode: codes.NotFound,
		wantConfig:  `{}`,
	}}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			runTestSet(t, model, tc)
		})
	}
}

func TestUpdate(t *testing.T) {
	tests := []gnmiSetTestCase{{
		desc: "update leaf node",
		initConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
		op: pb.UpdateResult_UPDATE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "config" >
			elem: <name: "domain-name" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_StringVal{StringVal: "foo.bar.com"},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"config": {
					"domain-name": "foo.bar.com",
					"hostname": "switch_a"
				}
			}
		}`,
	}, {
		desc: "update subtree",
		initConfig: `{
			"system": {
				"config": {
					"hostname": "switch_a"
				}
			}
		}`,
		op: pb.UpdateResult_UPDATE,
		textPbPath: `
			elem: <name: "system" >
			elem: <name: "config" >
		`,
		val: &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{
				JsonIetfVal: []byte(`{"domain-name": "foo.bar.com", "hostname": "switch_a"}`),
			},
		},
		wantRetCode: codes.OK,
		wantConfig: `{
			"system": {
				"config": {
					"domain-name": "foo.bar.com",
					"hostname": "switch_a"
				}
			}
		}`,
	}}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			runTestSet(t, model, tc)
		})
	}
}

func runTestSet(t *testing.T, m *Model, tc gnmiSetTestCase) {
	// Create a new server with empty config
	s, err := NewServer(m, []byte(tc.initConfig), nil)
	if err != nil {
		t.Fatalf("error in creating config server: %v", err)
	}

	// Send request
	var pbPath pb.Path
	if err := proto.UnmarshalText(tc.textPbPath, &pbPath); err != nil {
		t.Fatalf("error in unmarshaling path: %v", err)
	}
	var req *pb.SetRequest
	switch tc.op {
	case pb.UpdateResult_DELETE:
		req = &pb.SetRequest{Delete: []*pb.Path{&pbPath}}
	case pb.UpdateResult_REPLACE:
		req = &pb.SetRequest{Replace: []*pb.Update{{Path: &pbPath, Val: tc.val}}}
	case pb.UpdateResult_UPDATE:
		req = &pb.SetRequest{Update: []*pb.Update{{Path: &pbPath, Val: tc.val}}}
	default:
		t.Fatalf("invalid op type: %v", tc.op)
	}
	_, err = s.Set(nil, req)

	// Check return code
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	if gotRetStatus.Code() != tc.wantRetCode {
		t.Fatalf("got return code %v, want %v\nerror message: %v", gotRetStatus.Code(), tc.wantRetCode, err)
	}

	// Check server config
	wantConfigStruct, err := m.NewConfigStruct([]byte(tc.wantConfig))
	if err != nil {
		t.Fatalf("wantConfig data cannot be loaded as a config struct: %v", err)
	}
	wantConfigJSON, err := ygot.ConstructIETFJSON(wantConfigStruct, &ygot.RFC7951JSONConfig{})
	if err != nil {
		t.Fatalf("error in constructing IETF JSON tree from wanted config: %v", err)
	}
	gotConfigJSON, err := ygot.ConstructIETFJSON(s.config, &ygot.RFC7951JSONConfig{})
	if err != nil {
		t.Fatalf("error in constructing IETF JSON tree from server config: %v", err)
	}
	if !reflect.DeepEqual(gotConfigJSON, wantConfigJSON) {
		t.Fatalf("got server config %v\nwant: %v", gotConfigJSON, wantConfigJSON)
	}
}

func TestSubscribeOnce(t *testing.T) {
	jsonConfigRoot := `{
		"openconfig-system:system": {
			"openconfig-openflow:openflow": {
				"agent": {
					"state": {
						"failure-mode": "SECURE",
						"max-backoff": 10
					}
				}
			}
		},
	"openconfig-platform:components": {
	    "component": [
	      {
	        "state": {
			  "oper-status": "ACTIVE"
	        },
	        "name": "swpri1-1-1",
			"config": {
				"name": "swpri1-1-1"
			}
	      },
		  {
	        "state": {
			  "oper-status": "INACTIVE"
	        },
	        "name": "swpri2-2-2",
			"config": {
				"name": "swpri2-2-2"
			}
	      }
	    ]
	}
}`
	pathAgentState := &pb.Path{
		Elem: []*pb.PathElem{
			&pb.PathElem{Name: "system"},
			&pb.PathElem{Name: "openflow"},
			&pb.PathElem{Name: "agent"},
			&pb.PathElem{Name: "state"},
		}}
	pathAgentFailureMode := proto.Clone(pathAgentState).(*pb.Path)
	pathAgentFailureMode.Elem = append(pathAgentFailureMode.Elem, &pb.PathElem{Name: "failure-mode"})
	pathAgentMaxBackoff := proto.Clone(pathAgentState).(*pb.Path)
	pathAgentMaxBackoff.Elem = append(pathAgentMaxBackoff.Elem, &pb.PathElem{Name: "max-backoff"})
	pathAgentFoo := proto.Clone(pathAgentState).(*pb.Path)
	pathAgentFoo.Elem = append(pathAgentMaxBackoff.Elem, &pb.PathElem{Name: "foo"})
	pathComponentSw1State := &pb.Path{
		Elem: []*pb.PathElem{
			&pb.PathElem{Name: "components"},
			&pb.PathElem{Name: "component", Key: map[string]string{"name": "swpri1-1-1"}},
			&pb.PathElem{Name: "state"},
		}}
	pathComponentSw2Oper := &pb.Path{
		Elem: []*pb.PathElem{
			&pb.PathElem{Name: "components"},
			&pb.PathElem{Name: "component", Key: map[string]string{"name": "swpri2-2-2"}},
			&pb.PathElem{Name: "state"},
			&pb.PathElem{Name: "oper-status"},
		}}
	pathComponentSw3State := &pb.Path{
		Elem: []*pb.PathElem{
			&pb.PathElem{Name: "components"},
			&pb.PathElem{Name: "component", Key: map[string]string{"name": "swpri3-3-3"}},
			&pb.PathElem{Name: "state"},
		}}
	pathComponentStartOper := &pb.Path{
		Elem: []*pb.PathElem{
			&pb.PathElem{Name: "components"},
			&pb.PathElem{Name: "component", Key: map[string]string{"name": "*"}},
			&pb.PathElem{Name: "state"},
			&pb.PathElem{Name: "oper-status"},
		}}
	pathComponentStartState := proto.Clone(pathComponentStartOper).(*pb.Path)
	pathComponentStartState.Elem = pathComponentStartState.Elem[:len(pathComponentStartState.Elem)-1]
	pathComponentSw1Oper := proto.Clone(pathComponentSw1State).(*pb.Path)
	pathComponentSw1Oper.Elem = append(pathComponentSw1Oper.Elem, &pb.PathElem{Name: "oper-status"})
	pathComponentSw1Star := proto.Clone(pathComponentSw1State).(*pb.Path)
	pathComponentSw1Star.Elem[len(pathComponentSw1Star.Elem)-1].Name = "*"

	s, err := NewServer(model, []byte(jsonConfigRoot), nil)
	if err != nil {
		t.Fatalf("error in creating server: %v", err)
	}

	tests := []struct {
		desc              string
		subscriptions     []*pb.Subscription
		pathPrefix        *pb.Path
		updatesOnly       bool
		wantError         error
		wantNotifications []*pb.Notification
	}{{
		desc: "Subscribe to leaf node",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathAgentFailureMode}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentFailureMode,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}}}}},
	}, {
		desc: "Subscribe to multiple leaf nodes",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathAgentFailureMode},
			&pb.Subscription{
				Path: pathAgentMaxBackoff}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentFailureMode,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}}}},
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentMaxBackoff,
						Val:  &pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: uint64(10)}}}}}},
	}, {
		desc: "Subscribe to container node",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathAgentState}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentFailureMode,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}},
					&pb.Update{
						Path: pathAgentMaxBackoff,
						Val:  &pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: uint64(10)}}}}}},
	}, {
		desc: "Subscribe to container and leaf nodes",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathAgentState},
			&pb.Subscription{
				Path: pathAgentFailureMode},
		},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentFailureMode,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}},
					&pb.Update{
						Path: pathAgentMaxBackoff,
						Val:  &pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: uint64(10)}}}}},
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentFailureMode,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}}}}},
	}, {
		desc:       "Subscribe with prefix",
		pathPrefix: pathAgentState,
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: &pb.Path{Elem: []*pb.PathElem{&pb.PathElem{Name: "failure-mode"}}}}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathAgentFailureMode,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}}}}},
	}, {
		desc: "Subscribe to keyed path",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathComponentSw1Oper}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathComponentSw1Oper,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "ACTIVE"}}}}}},
	}, {
		desc: "Subscribe to leaf node with wildcard key",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathComponentStartOper}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathComponentSw1Oper,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "ACTIVE"}}},
					&pb.Update{
						Path: pathComponentSw2Oper,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "INACTIVE"}}}}}},
	}, {
		desc: "Subscribe to container node with wildcard key",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathComponentStartState}},
		wantNotifications: []*pb.Notification{
			&pb.Notification{
				Update: []*pb.Update{
					&pb.Update{
						Path: pathComponentSw1Oper,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "ACTIVE"}}},
					&pb.Update{
						Path: pathComponentSw2Oper,
						Val: &pb.TypedValue{
							Value: &pb.TypedValue_StringVal{StringVal: "INACTIVE"}}}}}},
	}, {
		desc: "Subscribe to wildcard path element",
		subscriptions: []*pb.Subscription{
			&pb.Subscription{
				Path: pathComponentSw1Star}},
		wantError: status.Errorf(codes.InvalidArgument, "No match found for path elem: <name: *>"),
	}, {
		desc:          "Subscribe to not-found path",
		subscriptions: []*pb.Subscription{&pb.Subscription{Path: pathAgentFoo}},
		wantError:     status.Errorf(codes.NotFound, "path %v not found", pathAgentFoo),
	}, {
		desc:          "Subscribe to not-found key",
		subscriptions: []*pb.Subscription{&pb.Subscription{Path: pathComponentSw3State}},
		wantError:     status.Errorf(codes.NotFound, "path %v not found", pathComponentSw3State),
	}, {
		desc:          "Subscribe to updates only",
		subscriptions: []*pb.Subscription{&pb.Subscription{Path: pathAgentState}},
		updatesOnly:   true,
	}}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			runTestSubscribeOnce(t, s, test.subscriptions, test.pathPrefix, test.wantError, test.wantNotifications, test.updatesOnly)
		})
	}
}

// runTestSubscribeOnce requests a ONCE subscription to a path, and compares the returned Updates.
func runTestSubscribeOnce(t *testing.T, s *Server, subscribptions []*pb.Subscription, pathPrefix *pb.Path, wantError error, wantNotifications []*pb.Notification, updatesOnly bool) {

	req := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix:       pathPrefix,
				Mode:         pb.SubscriptionList_ONCE,
				UpdatesOnly:  updatesOnly,
				Subscription: subscribptions,
			},
		},
	}

	errC := make(chan error)
	defer close(errC)
	msgQ := coalesce.NewQueue()
	c := &streamClient{sr: req, stream: nil, errC: errC, msgQ: msgQ}

	go s.doOnceSubscription(c)
	go func() {
		for {
			time.Sleep(time.Second)
			if msgQ.IsClosed() {
				errC <- nil
				return
			}
		}
	}()

	gotErr := <-errC
	if gotErr != nil && wantError == nil {
		t.Fatalf("got a error, wanted nil: %v", gotErr)
	}
	if wantError != nil {
		if gotErr == nil {
			t.Fatalf("wanted an error, got nil: %v", wantError)
		}
		if errors.Is(gotErr, wantError) {
			t.Fatalf("wanted error: %v, got error: %v", wantError, gotErr)
		}
		return
	}

	// Check response value
	gotSync := false
	var gotNotifications []*pb.Notification
	for {
		msg, _, err := c.msgQ.Next(context.Background())
		if err != nil {
			if coalesce.IsClosedQueue(err) {
				break
			} else {
				t.Fatalf("Error getting message from the queue: %v", err)
			}
		}

		if _, ok := msg.(subscribeSyncToken); ok {
			gotSync = true
			continue
		}

		n, ok := msg.(*pb.Notification)
		if !ok || n == nil {
			t.Fatalf("invalid message in queue: %v", msg)
		}
		gotNotifications = append(gotNotifications, n)
	}
	if diff := cmp.Diff(gotNotifications, wantNotifications, protocmp.Transform(),
		protocmp.SortRepeated(updateLess),
		protocmp.IgnoreFields(&pb.Notification{}, "timestamp")); diff != "" {
		t.Errorf("Updates diff:\n%v", diff)
	}
	if !gotSync {
		t.Errorf("did not receive sync_response message")
	}
}

func TestSubscribeSample(t *testing.T) {
	jsonConfigRoot := `{
		"openconfig-system:system": {
			"openconfig-openflow:openflow": {
				"agent": {
					"state": {
						"failure-mode": "SECURE",
						"max-backoff": 10
					}
				}
			}
		}
}`
	pathAgentState := &pb.Path{
		Elem: []*pb.PathElem{
			&pb.PathElem{Name: "system"},
			&pb.PathElem{Name: "openflow"},
			&pb.PathElem{Name: "agent"},
			&pb.PathElem{Name: "state"},
		}}
	pathAgentFailureMode := proto.Clone(pathAgentState).(*pb.Path)
	pathAgentFailureMode.Elem = append(pathAgentFailureMode.Elem, &pb.PathElem{Name: "failure-mode"})
	pathAgentMaxBackoff := proto.Clone(pathAgentState).(*pb.Path)
	pathAgentMaxBackoff.Elem = append(pathAgentMaxBackoff.Elem, &pb.PathElem{Name: "max-backoff"})
	pathAgentFoo := proto.Clone(pathAgentState).(*pb.Path)
	pathAgentFoo.Elem = append(pathAgentMaxBackoff.Elem, &pb.PathElem{Name: "foo"})

	secsToNanoSecs := func(s int) uint64 { return uint64(s * 1000000000) }

	s, err := NewServer(model, []byte(jsonConfigRoot), nil)
	if err != nil {
		t.Fatalf("error in creating server: %v", err)
	}

	tests := []struct {
		desc              string
		pathPrefix        *pb.Path
		subscription      *pb.Subscription
		timeout           time.Duration
		updatesOnly       bool
		wantUpdates       []*pb.Update
		wantNotifications int
	}{{
		desc: "Single Sample Subscription",
		subscription: &pb.Subscription{
			Mode:           pb.SubscriptionMode_SAMPLE,
			SampleInterval: secsToNanoSecs(1),
			Path:           pathAgentFailureMode},
		timeout:           time.Millisecond * 3500,
		wantNotifications: 4,
		wantUpdates: []*pb.Update{
			&pb.Update{
				Path: pathAgentFailureMode,
				Val: &pb.TypedValue{
					Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}},
		},
	}, {
		desc: "Single Subscribe to Update Only with 0 interval",
		subscription: &pb.Subscription{
			Mode:           pb.SubscriptionMode_SAMPLE,
			SampleInterval: 0,
			Path:           pathAgentState},
		timeout:           time.Millisecond * 3500,
		wantNotifications: 3,
		updatesOnly:       true,
		wantUpdates: []*pb.Update{
			&pb.Update{
				Path: pathAgentFailureMode,
				Val: &pb.TypedValue{
					Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}},
			&pb.Update{
				Path: pathAgentMaxBackoff,
				Val:  &pb.TypedValue{Value: &pb.TypedValue_UintVal{UintVal: uint64(10)}}},
		},
	}, {
		desc:       "Subscribe with prefix",
		pathPrefix: pathAgentState,
		subscription: &pb.Subscription{
			Mode:           pb.SubscriptionMode_SAMPLE,
			SampleInterval: secsToNanoSecs(2),
			Path:           &pb.Path{Elem: []*pb.PathElem{&pb.PathElem{Name: "failure-mode"}}}},
		timeout:           time.Millisecond * 4500,
		wantNotifications: 3,
		wantUpdates: []*pb.Update{
			&pb.Update{
				Path: pathAgentFailureMode,
				Val: &pb.TypedValue{
					Value: &pb.TypedValue_StringVal{StringVal: "SECURE"}}},
		},
	}}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			runTestSubscribeSample(t, s, test.subscription, test.pathPrefix, test.updatesOnly, test.timeout, test.wantNotifications, test.wantUpdates)
		})
	}
}

// runTestSubscribeSample requests STREAM sampling subscription, and compares the returned Notifications.
func runTestSubscribeSample(t *testing.T, s *Server, subscription *pb.Subscription, pathPrefix *pb.Path, updatesOnly bool, timeout time.Duration, wantNotifications int, wantUpdates []*pb.Update,
) {
	req := &pb.SubscribeRequest{
		Request: &pb.SubscribeRequest_Subscribe{
			Subscribe: &pb.SubscriptionList{
				Prefix:       pathPrefix,
				Mode:         pb.SubscriptionList_STREAM,
				UpdatesOnly:  updatesOnly,
				Subscription: []*pb.Subscription{subscription},
			},
		},
	}

	sampleInterval := subscription.GetSampleInterval()
	if sampleInterval == 0 {
		sampleInterval = uint64(minStreamSampleInterval.Nanoseconds())
	}
	interval := time.Nanosecond * time.Duration(sampleInterval)

	errC := make(chan error)
	doneC := make(chan bool)
	defer close(errC)
	msgQ := coalesce.NewQueue()
	c := &streamClient{sr: req, stream: nil, errC: errC, msgQ: msgQ}

	go s.doSampleSubscription(c, subscription, doneC)

	time.Sleep(timeout)
	close(doneC)
	msgQ.Close()

	gotNotifications := 0
	lastNotificationTimestamp := int64(time.Now().UnixNano())

	if !updatesOnly && wantNotifications > 0 {
		msg, _, err := c.msgQ.Next(context.Background())
		if err != nil {
			t.Fatalf("Error getting initial Notification from the queue: %v", err)
		}
		n, ok := msg.(*pb.Notification)
		if !ok || n == nil {
			t.Fatalf("wanted Notification message in queue, got: %v", msg)
		}
		lastNotificationTimestamp = n.GetTimestamp()
		gotNotifications = gotNotifications + 1
		if diff := cmp.Diff(n.GetUpdate(), wantUpdates, protocmp.Transform(), protocmp.SortRepeated(updateLess)); diff != "" {
			t.Errorf("Initial Notification Updates diff:\n%v", diff)
		}
	}

	msg, _, err := c.msgQ.Next(context.Background())
	if err != nil {
		t.Fatalf("Error getting sync_response from the queue: %v", err)
	}
	if _, ok := msg.(subscribeSyncToken); !ok {
		t.Fatalf("did not receive sync_response message")
	}

	for {
		msg, _, err := c.msgQ.Next(context.Background())
		if err != nil {
			if coalesce.IsClosedQueue(err) {
				break
			} else {
				t.Fatalf("Error getting Notifications from the queue: %v", err)
			}
		}

		if _, ok := msg.(subscribeSyncToken); ok {
			t.Fatalf("received unexpected sync_response message")
		}

		n, ok := msg.(*pb.Notification)
		if !ok || n == nil {
			t.Fatalf("wanted Notification message in queue, got: %v", msg)
		}
		gotNotifications = gotNotifications + 1
		timeDiff := time.Nanosecond * time.Duration(n.GetTimestamp()-lastNotificationTimestamp)
		lastNotificationTimestamp = n.GetTimestamp()
		if (timeDiff - interval) > interval {
			t.Errorf("Notification messages not within sampling interval: %v", timeDiff)
		}
		if diff := cmp.Diff(n.GetUpdate(), wantUpdates, protocmp.Transform(), cmpopts.SortSlices(updateLess)); diff != "" {
			t.Errorf("Notification Updates diff:\n%v", diff)
		}
	}

	if diff := cmp.Diff(wantNotifications, gotNotifications); diff != "" {
		t.Errorf("wanted %d Notifications, got %d", wantNotifications, gotNotifications)
	}

}

// updateLess compares 2 Update messages by the string comparison of their Paths.
func updateLess(a, b *pb.Update) bool {
	pathA, err := ygot.PathToString(a.GetPath())
	if err != nil {
		return false
	}
	pathB, err := ygot.PathToString(b.GetPath())
	if err != nil {
		return true
	}
	return pathA < pathB
}

// jsonBytesEqual is a helper function to compare two json strings for
// equality.
func jsonBytesEqual(a, b []byte) (bool, error) {
	var j, j2 interface{}
	if err := json.Unmarshal(a, &j); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &j2); err != nil {
		return false, err
	}
	return reflect.DeepEqual(j2, j), nil
}

// TestConfigAsJSON validates that the method `ConfigAsJSON` returns a json
// string that meets the running configuration of the server in a fashion that
// can be loaded back into the server.
func TestConfigToJSON(t *testing.T) {
        jsonConfigRoot := `{
          "openconfig-system:system": {
            "openconfig-openflow:openflow": {
              "agent": {
                "config": {
                  "failure-mode": "SECURE",
                  "max-backoff": 10
                }
              }
            }
          },
          "openconfig-platform:components": {
            "component": [
              {
                "config": {
                  "name": "swpri1-1-1"
                },
                "name": "swpri1-1-1"
              }
            ]
          }
        }`

	s, err := NewServer(model, []byte(jsonConfigRoot), nil)
	if err != nil {
		t.Fatalf("error in creating server: %v", err)
	}

	res, err := s.ConfigAsJSON()
	if err != nil {
		t.Fatalf("error in creating json from model: %v", err)
	}

	areEqual, err := jsonBytesEqual([]byte(jsonConfigRoot), []byte(res))
	if err != nil {
		t.Fatalf("error in comparing json bytes: %v", err)
	}

	if (!areEqual) {
		t.Errorf("config mismatch!\n Got: %s\n Wanted: %s", res, jsonConfigRoot)
	}

}
