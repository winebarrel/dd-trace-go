// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var currentM *testing.M
var mTracer mocktracer.Tracer

// TestMain is the entry point for testing and runs before any test.
func TestMain(m *testing.M) {
	log.SetLevel(log.LevelDebug)

	// We need to spawn separated test process for each scenario
	scenarios := []string{"TestFlakyTestRetries", "TestEarlyFlakeDetection", "TestFlakyTestRetriesAndEarlyFlakeDetection", "TestIntelligentTestRunner", "TestManagementTests"}

	if internal.BoolEnv(scenarios[0], false) {
		fmt.Printf("Scenario %s started.\n", scenarios[0])
		runFlakyTestRetriesTests(m)
	} else if internal.BoolEnv(scenarios[1], false) {
		fmt.Printf("Scenario %s started.\n", scenarios[1])
		runEarlyFlakyTestDetectionTests(m)
	} else if internal.BoolEnv(scenarios[2], false) {
		fmt.Printf("Scenario %s started.\n", scenarios[2])
		runFlakyTestRetriesWithEarlyFlakyTestDetectionTests(m)
	} else if internal.BoolEnv(scenarios[3], false) {
		fmt.Printf("Scenario %s started.\n", scenarios[3])
		runIntelligentTestRunnerTests(m)
	} else if internal.BoolEnv(scenarios[4], false) {
		fmt.Printf("Scenario %s started.\n", scenarios[4])
		runTestManagementTests(m)
	} else {
		fmt.Println("Starting tests...")
		for _, v := range scenarios {
			cmd := exec.Command(os.Args[0], os.Args[1:]...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			cmd.Env = append(cmd.Env, os.Environ()...)
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=true", v))
			fmt.Printf("Running scenario: %s:\n", v)
			err := cmd.Run()
			fmt.Printf("Done.\n\n")
			if err != nil {
				if exiterr, ok := err.(*exec.ExitError); ok {
					fmt.Printf("Scenario %s failed with exit code: %d\n", v, exiterr.ExitCode())
					os.Exit(exiterr.ExitCode())
				} else {
					fmt.Printf("cmd.Run: %v\n", err)
					os.Exit(1)
				}
				break
			}
		}
	}

	os.Exit(0)
}

func runFlakyTestRetriesTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHttpServer(true, true, false, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
			},
		},
	},
		false, nil,
		false, nil)
	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + fmt.Sprintf("%d", exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02 + 2 subtests
	// 1 Test_Foo + 3 subtests
	// 1 TestSkip
	// 1 TestRetryWithPanic + 3 retry tests from testing_test.go
	// 1 TestRetryWithFail + 3 retry tests from testing_test.go
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 1 TestEarlyFlakeDetection
	// 3 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic(fmt.Sprintf("source file should be testify_test.go, got %s", testifySub01.Tag("test.source.file").(string)))
	}

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 6)
	trrSpan := checkSpansByTagName(finishedSpans, constants.TestRetryReason, 6)[0]
	if trrSpan.Tag(constants.TestRetryReason) != "atr" {
		panic(fmt.Sprintf("expected retry reason to be %s, got %s", "atr", trrSpan.Tag(constants.TestRetryReason)))
	}

	// check the test is new tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 22)

	// check spans by type
	checkSpansByType(finishedSpans,
		28,
		1,
		1,
		4,
		27,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func runEarlyFlakyTestDetectionTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHttpServer(false, true, true, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
			},
		},
	},
		false, nil,
		false, nil)
	defer server.Close()

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + fmt.Sprintf("%d", exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 11 TestMyTest01
	// 11 TestMyTest02 + 22 subtests
	// 11 Test_Foo + 33 subtests
	// 11 TestSkip
	// 11 TestRetryWithPanic
	// 11 TestRetryWithFail
	// 11 TestNormalPassingAfterRetryAlwaysFail
	// 11 TestEarlyFlakeDetection
	// 22 normal spans from testing_test.go
	// 33 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 11)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 11)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 11)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 11)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 11)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic(fmt.Sprintf("source file should be testify_test.go, got %s", testifySub01.Tag("test.source.file").(string)))
	}

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 176)
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 160)
	trrSpan := checkSpansByTagName(finishedSpans, constants.TestRetryReason, 160)[0]
	if trrSpan.Tag(constants.TestRetryReason) != "efd" {
		panic(fmt.Sprintf("expected retry reason to be %s, got %s", "efd", trrSpan.Tag(constants.TestRetryReason)))
	}

	// check spans by type
	checkSpansByType(finishedSpans,
		152,
		1,
		1,
		4,
		181,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func runFlakyTestRetriesWithEarlyFlakyTestDetectionTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHttpServer(true, true, true, &net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting": net.KnownTestsResponseDataSuites{
				"reflections_test.go": []string{
					"TestGetFieldPointerFrom",
					"TestGetInternalTestArray",
					"TestGetInternalBenchmarkArray",
					"TestCommonPrivateFields_AddLevel",
					"TestGetBenchmarkPrivateFields",
				},
				"testing_test.go": []string{
					"TestMyTest01",
					"TestMyTest02",
					"Test_Foo",
					"TestWithExternalCalls",
					"TestSkip",
					"TestRetryWithPanic",
					"TestRetryWithFail",
					"TestRetryAlwaysFail",
					"TestNormalPassingAfterRetryAlwaysFail",
				},
				"testify_test.go": []string{
					"TestTestifyLikeTest",
				},
				"testify_test.go/MySuite": []string{
					"TestTestifyLikeTest/TestMySuite",
					"TestTestifyLikeTest/TestMySuite/sub01",
				},
			},
		},
	},
		false, nil,
		false, nil)
	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + fmt.Sprintf("%d", exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02 + 2 subtests
	// 1 Test_Foo + 3 subtests
	// 1 TestWithExternalCalls + 2 subtests
	// 1 TestSkip
	// 1 TestRetryWithPanic + 3 retry tests from testing_test.go
	// 1 TestRetryWithFail + 3 retry tests from testing_test.go
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 1 TestEarlyFlakeDetection + 10 EFD retries
	// 2 normal spans from testing_test.go
	// 3 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 4)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 11)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic(fmt.Sprintf("source file should be testify_test.go, got %s", testifySub01.Tag("test.source.file").(string)))
	}

	// check spans by tag
	checkSpansByTagName(finishedSpans, constants.TestIsNew, 11)
	checkSpansByTagName(finishedSpans, constants.TestIsRetry, 16)

	// check spans by type
	checkSpansByType(finishedSpans,
		38,
		1,
		1,
		4,
		37,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func runIntelligentTestRunnerTests(m *testing.M) {
	// mock the settings api to enable automatic test retries
	server := setUpHttpServer(true, true, false, nil, true, []net.SkippableResponseDataAttributes{
		{
			Suite: "testing_test.go",
			Name:  "TestMyTest01",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestMyTest02",
		},
		{
			Suite: "testing_test.go",
			Name:  "Test_Foo",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryWithPanic",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryWithFail",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestRetryAlwaysFail",
		},
		{
			Suite: "testing_test.go",
			Name:  "TestNormalPassingAfterRetryAlwaysFail",
		},
	},
		false, nil)
	defer server.Close()

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, we are expecting some tests to fail and check the assertion later
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. All tests should pass (failed ones should be skipped by ITR).")
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()

	// 1 session span
	// 1 module span
	// 4 suite span (testing_test.go, testify_test.go, testify_test.go/MySuite and reflections_test.go)
	// 5 tests from reflections_test.go
	// 1 TestMyTest01
	// 1 TestMyTest02
	// 1 Test_Foo
	// 1 TestSkip
	// 1 TestRetryWithPanic
	// 1 TestRetryWithFail
	// 1 TestRetryAlwaysFail
	// 1 TestNormalPassingAfterRetryAlwaysFail
	// 1 TestEarlyFlakeDetection
	// 3 tests from testify_test.go and testify_test.go/MySuite

	// check spans by resource name
	checkSpansByResourceName(finishedSpans, "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetFieldPointerFrom", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalTestArray", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalBenchmarkArray", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestCommonPrivateFields_AddLevel", 1)
	checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetBenchmarkPrivateFields", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest02/sub01/sub03", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/yellow_should_return_color", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/banana_should_return_fruit", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.Test_Foo/duck_should_return_animal", 0)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestSkip", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestNormalPassingAfterRetryAlwaysFail", 1)
	checkSpansByResourceName(finishedSpans, "testing_test.go.TestEarlyFlakeDetection", 1)
	checkSpansByResourceName(finishedSpans, "testify_test.go.TestTestifyLikeTest", 1)
	testifySub01 := checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite", 1)[0]
	checkSpansByResourceName(finishedSpans, "testify_test.go/MySuite.TestTestifyLikeTest/TestMySuite/sub01", 1)

	// check that testify span has the correct source file
	if !strings.HasSuffix(testifySub01.Tag("test.source.file").(string), "/testify_test.go") {
		panic(fmt.Sprintf("source file should be testify_test.go, got %s", testifySub01.Tag("test.source.file").(string)))
	}

	// check ITR spans
	// 5 tests skipped by ITR and 1 normal skipped test
	checkSpansByTagValue(finishedSpans, constants.TestStatus, constants.TestStatusSkip, 6)
	checkSpansByTagValue(finishedSpans, constants.TestSkipReason, constants.SkippedByITRReason, 5)

	// check unskippable tests
	// 5 tests from unskippable suite in reflections_test.go and 2 unskippable tests from testing_test.go
	checkSpansByTagValue(finishedSpans, constants.TestUnskippable, "true", 7)
	checkSpansByTagValue(finishedSpans, constants.TestForcedToRun, "true", 1)

	// check spans by type
	checkSpansByType(finishedSpans,
		17,
		1,
		1,
		4,
		16,
		0)

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func runTestManagementTests(m *testing.M) {
	// mock the settings api to enable quarantine and disable tests
	server := setUpHttpServer(false, false, false, nil, false, nil, true,
		&net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"reflections_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"TestGetFieldPointerFrom": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined:  true,
										AttemptToFix: true,
									},
								},
								"TestGetInternalTestArray": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Disabled:     true,
										AttemptToFix: true,
									},
								},
							},
						},
						"testing_test.go": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"TestMyTest01": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Disabled: true,
									},
								},
								"TestRetryWithFail": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Quarantined: true,
									},
								},
								"TestRetryWithPanic": {
									Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{
										Disabled:     true,
										AttemptToFix: true,
									},
								},
							},
						},
					},
				},
			},
		})

	defer server.Close()

	// set a custom retry count
	os.Setenv(constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	testRetryWithPanicRunNumber = -10 // this makes TestRetryWithPanic to always fail (required by this test)
	exitCode := RunM(m)
	if exitCode != 0 {
		panic("expected the exit code to be 0. Got exit code: " + fmt.Sprintf("%d", exitCode))
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()

	// Disabled test with an attempt to fix with 10 executions
	testGetInternalTestArray := checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetInternalTestArray", 10)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestIsDisabled, "true", 10)               // Disabled
	checkSpansByTagValue(testGetInternalTestArray, constants.TestIsAttempToFix, "true", 10)            // Is an attempt to fix
	checkSpansByTagValue(testGetInternalTestArray, constants.TestIsRetry, "true", 9)                   // 9 retries
	checkSpansByTagValue(testGetInternalTestArray, constants.TestRetryReason, "attempt_to_fix", 9)     // 9 retries with the attempt to fix reason
	checkSpansByTagValue(testGetInternalTestArray, constants.TestAttemptToFixPassed, "true", 1)        // Attempt to fix passed (reported in the latest retry)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestHasFailedAllRetries, "true", 0)       // All retries failed = false (reported in the latest retry)
	checkSpansByTagValue(testGetInternalTestArray, constants.TestStatus, constants.TestStatusPass, 10) // All tests passed

	// Quaratined test with an attempt to fix with 10 executions
	testGetFieldPointerFrom := checkSpansByResourceName(finishedSpans, "reflections_test.go.TestGetFieldPointerFrom", 10)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestIsQuarantined, "true", 10)            // Quarantined
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestIsAttempToFix, "true", 10)            // Is an attempt to fix
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestIsRetry, "true", 9)                   // 9 retries
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestRetryReason, "attempt_to_fix", 9)     // 9 retries with the attempt to fix reason
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestAttemptToFixPassed, "true", 1)        // Attempt to fix passed (reported in the latest retry)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestHasFailedAllRetries, "true", 0)       // All retries failed = false (reported in the latest retry)
	checkSpansByTagValue(testGetFieldPointerFrom, constants.TestStatus, constants.TestStatusPass, 10) // All tests passed

	// Disabled test without an attempt to fix (it just skipped and reported as skipped)
	testMyTest01 := checkSpansByResourceName(finishedSpans, "testing_test.go.TestMyTest01", 1)
	checkSpansByTagValue(testMyTest01, constants.TestIsDisabled, "true", 1)               // Disabled
	checkSpansByTagValue(testMyTest01, constants.TestIsAttempToFix, "true", 0)            // Is not an attempt to fix
	checkSpansByTagValue(testMyTest01, constants.TestIsRetry, "true", 0)                  // 0 retries
	checkSpansByTagValue(testMyTest01, constants.TestRetryReason, "attempt_to_fix", 0)    // 0 retries with the attempt to fix reason
	checkSpansByTagValue(testMyTest01, constants.TestHasFailedAllRetries, "true", 0)      // All retries failed (reported in the latest retry)
	checkSpansByTagValue(testMyTest01, constants.TestAttemptToFixPassed, "true", 0)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testMyTest01, constants.TestStatus, constants.TestStatusSkip, 1) // Because is not an attempt to fix we just skip it

	// Quarantined test without an attempt to fix (it executed but reported as skipped)
	testRetryWithFail := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithFail", 1)
	checkSpansByTagValue(testRetryWithFail, constants.TestIsQuarantined, "true", 1)            // Quarantined
	checkSpansByTagValue(testRetryWithFail, constants.TestIsAttempToFix, "true", 0)            // Is not an attempt to fix
	checkSpansByTagValue(testRetryWithFail, constants.TestIsRetry, "true", 0)                  // 0 retries
	checkSpansByTagValue(testRetryWithFail, constants.TestRetryReason, "attempt_to_fix", 0)    // 0 retries with the attempt to fix reason
	checkSpansByTagValue(testRetryWithFail, constants.TestHasFailedAllRetries, "true", 0)      // All retries failed (reported in the latest retry)
	checkSpansByTagValue(testRetryWithFail, constants.TestAttemptToFixPassed, "true", 0)       // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testRetryWithFail, constants.TestStatus, constants.TestStatusFail, 1) // Because is not an attempt to fix we execute it but don't report the status

	// Disabled test with an attempt to fix with 10 executions
	testRetryWithPanic := checkSpansByResourceName(finishedSpans, "testing_test.go.TestRetryWithPanic", 10)
	checkSpansByTagValue(testRetryWithPanic, constants.TestIsDisabled, "true", 10)               // Disabled
	checkSpansByTagValue(testRetryWithPanic, constants.TestIsAttempToFix, "true", 10)            // Is an attempt to fix
	checkSpansByTagValue(testRetryWithPanic, constants.TestIsRetry, "true", 9)                   // 9 retries
	checkSpansByTagValue(testRetryWithPanic, constants.TestRetryReason, "attempt_to_fix", 9)     // 9 retries with the attempt to fix reason
	checkSpansByTagValue(testRetryWithPanic, constants.TestHasFailedAllRetries, "true", 1)       // All retries failed (reported in the latest retry)
	checkSpansByTagValue(testRetryWithPanic, constants.TestAttemptToFixPassed, "true", 0)        // Attempt to fix passed false (reported in the latest retry)
	checkSpansByTagValue(testRetryWithPanic, constants.TestStatus, constants.TestStatusFail, 10) // All tests passed

	// check capabilities tags
	checkCapabilitiesTags(finishedSpans)

	fmt.Println("All tests passed.")
	os.Exit(0)
}

func checkSpansByType(finishedSpans []mocktracer.Span,
	totalFinishedSpansCount int, sessionSpansCount int, moduleSpansCount int,
	suiteSpansCount int, testSpansCount int, normalSpansCount int) {
	calculatedFinishedSpans := len(finishedSpans)
	fmt.Printf("Number of spans received: %d\n", calculatedFinishedSpans)
	if calculatedFinishedSpans < totalFinishedSpansCount {
		panic(fmt.Sprintf("expected at least %d finished spans, got %d", totalFinishedSpansCount, calculatedFinishedSpans))
	}

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	calculatedSessionSpans := len(sessionSpans)
	fmt.Printf("Number of sessions received: %d\n", calculatedSessionSpans)
	showResourcesNameFromSpans(sessionSpans)
	if calculatedSessionSpans != sessionSpansCount {
		panic(fmt.Sprintf("expected exactly %d session span, got %d", sessionSpansCount, calculatedSessionSpans))
	}

	moduleSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestModule)
	calculatedModuleSpans := len(moduleSpans)
	fmt.Printf("Number of modules received: %d\n", calculatedModuleSpans)
	showResourcesNameFromSpans(moduleSpans)
	if calculatedModuleSpans != moduleSpansCount {
		panic(fmt.Sprintf("expected exactly %d module span, got %d", moduleSpansCount, calculatedModuleSpans))
	}

	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	calculatedSuiteSpans := len(suiteSpans)
	fmt.Printf("Number of suites received: %d\n", calculatedSuiteSpans)
	showResourcesNameFromSpans(suiteSpans)
	if calculatedSuiteSpans != suiteSpansCount {
		panic(fmt.Sprintf("expected exactly %d suite spans, got %d", suiteSpansCount, calculatedSuiteSpans))
	}

	testSpans := getSpansWithType(finishedSpans, constants.SpanTypeTest)
	calculatedTestSpans := len(testSpans)
	fmt.Printf("Number of tests received: %d\n", calculatedTestSpans)
	showResourcesNameFromSpans(testSpans)
	if calculatedTestSpans != testSpansCount {
		panic(fmt.Sprintf("expected exactly %d test spans, got %d", testSpansCount, calculatedTestSpans))
	}

	normalSpans := getSpansWithType(finishedSpans, ext.SpanTypeHTTP)
	calculatedNormalSpans := len(normalSpans)
	fmt.Printf("Number of http spans received: %d\n", calculatedNormalSpans)
	showResourcesNameFromSpans(normalSpans)
	if calculatedNormalSpans != normalSpansCount {
		panic(fmt.Sprintf("expected exactly %d normal spans, got %d", normalSpansCount, calculatedNormalSpans))
	}
}

func checkSpansByResourceName(finishedSpans []mocktracer.Span, resourceName string, count int) []mocktracer.Span {
	spans := getSpansWithResourceName(finishedSpans, resourceName)
	numOfSpans := len(spans)
	if numOfSpans != count {
		panic(fmt.Sprintf("expected exactly %d spans with resource name: %s, got %d", count, resourceName, numOfSpans))
	}

	return spans
}

func checkSpansByTagName(finishedSpans []mocktracer.Span, tagName string, count int) []mocktracer.Span {
	spans := getSpansWithTagName(finishedSpans, tagName)
	numOfSpans := len(spans)
	if numOfSpans != count {
		panic(fmt.Sprintf("expected exactly %d spans with tag name: %s, got %d", count, tagName, numOfSpans))
	}

	return spans
}

func checkSpansByTagValue(finishedSpans []mocktracer.Span, tagName, tagValue string, count int) []mocktracer.Span {
	spans := getSpansWithTagNameAndValue(finishedSpans, tagName, tagValue)
	numOfSpans := len(spans)
	if numOfSpans != count {
		panic(fmt.Sprintf("expected exactly %d spans with tag name: %s and value %s, got %d", count, tagName, tagValue, numOfSpans))
	}

	return spans
}

func checkCapabilitiesTags(finishedSpans []mocktracer.Span) {
	tests := getSpansWithType(finishedSpans, constants.SpanTypeTest)
	numOfTests := len(tests)
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestImpactAnalysis)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestImpactAnalysis))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesEarlyFlakeDetection)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesEarlyFlakeDetection))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesAutoTestRetries)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesAutoTestRetries))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestManagementQuarantine)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestManagementQuarantine))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestManagementDisable)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestManagementDisable))
	}
	if len(getSpansWithTagName(tests, constants.LibraryCapabilitiesTestManagementAttemptToFix)) != numOfTests {
		panic(fmt.Sprintf("expected all test spans to have the %s tag", constants.LibraryCapabilitiesTestManagementAttemptToFix))
	}
}

type (
	skippableResponse struct {
		Meta skippableResponseMeta   `json:"meta"`
		Data []skippableResponseData `json:"data"`
	}

	skippableResponseMeta struct {
		CorrelationID string `json:"correlation_id"`
	}

	skippableResponseData struct {
		ID         string                              `json:"id"`
		Type       string                              `json:"type"`
		Attributes net.SkippableResponseDataAttributes `json:"attributes"`
	}
)

func setUpHttpServer(
	flakyRetriesEnabled bool,
	knownTestsEnabled bool,
	earlyFlakyDetectionEnabled bool,
	earlyFlakyDetectionData *net.KnownTestsResponseData,
	itrEnabled bool,
	itrData []net.SkippableResponseDataAttributes,
	testManagement bool,
	testManagementData *net.TestManagementTestsResponseDataModules) *httptest.Server {
	enableKnownTests := knownTestsEnabled || earlyFlakyDetectionEnabled
	// mock the settings api to enable automatic test retries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("MockApi received request: %s\n", r.URL.Path)

		// Settings request
		if r.URL.Path == "/api/v2/libraries/tests/services/setting" {
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                   `json:"id"`
					Type       string                   `json:"type"`
					Attributes net.SettingsResponseData `json:"attributes"`
				} `json:"data,omitempty"`
			}{}

			// let's enable flaky test retries
			response.Data.Attributes = net.SettingsResponseData{
				FlakyTestRetriesEnabled: flakyRetriesEnabled,
				ItrEnabled:              itrEnabled,
				TestsSkipping:           itrEnabled,
				KnownTestsEnabled:       enableKnownTests,
			}

			response.Data.Attributes.TestManagement.Enabled = testManagement

			response.Data.Attributes.EarlyFlakeDetection.Enabled = earlyFlakyDetectionEnabled
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = 10
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.TenS = 5
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 3
			response.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveM = 2

			fmt.Printf("MockApi sending response: %v\n", response)
			json.NewEncoder(w).Encode(&response)
		} else if enableKnownTests && r.URL.Path == "/api/v2/ci/libraries/tests" {
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                     `json:"id"`
					Type       string                     `json:"type"`
					Attributes net.KnownTestsResponseData `json:"attributes"`
				} `json:"data,omitempty"`
			}{}

			if earlyFlakyDetectionData != nil {
				response.Data.Attributes = *earlyFlakyDetectionData
			}

			fmt.Printf("MockApi sending response: %v\n", response)
			json.NewEncoder(w).Encode(&response)
		} else if r.URL.Path == "/api/v2/git/repository/search_commits" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("{}"))
		} else if r.URL.Path == "/api/v2/git/repository/packfile" {
			w.WriteHeader(http.StatusAccepted)
		} else if itrEnabled && r.URL.Path == "/api/v2/ci/tests/skippable" {
			w.Header().Set("Content-Type", "application/json")
			response := skippableResponse{
				Meta: skippableResponseMeta{
					CorrelationID: "correlation_id",
				},
				Data: []skippableResponseData{},
			}
			for i, data := range itrData {
				response.Data = append(response.Data, skippableResponseData{
					ID:         fmt.Sprintf("id_%d", i),
					Type:       "type",
					Attributes: data,
				})
			}
			fmt.Printf("MockApi sending response: %v\n", response)
			json.NewEncoder(w).Encode(&response)
		} else if r.URL.Path == "/api/v2/test/libraries/test-management/tests" {
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                                     `json:"id"`
					Type       string                                     `json:"type"`
					Attributes net.TestManagementTestsResponseDataModules `json:"attributes"`
				} `json:"data,omitempty"`
			}{}
			response.Data.Type = "ci_app_libraries_tests"
			response.Data.Attributes = *testManagementData
			fmt.Printf("MockApi sending response: %v\n", response)
			json.NewEncoder(w).Encode(&response)
		} else {
			http.NotFound(w, r)
		}
	}))

	// set the custom agentless url and the flaky retry count env-var
	fmt.Printf("Using mockapi at: %s\n", server.URL)
	os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	os.Setenv(constants.APIKeyEnvironmentVariable, "12345")

	return server
}

func getSpansWithType(spans []mocktracer.Span, spanType string) []mocktracer.Span {
	var result []mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.SpanType) == spanType {
			result = append(result, span)
		}
	}

	return result
}

func getSpansWithResourceName(spans []mocktracer.Span, resourceName string) []mocktracer.Span {
	var result []mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.ResourceName) == resourceName {
			result = append(result, span)
		}
	}

	return result
}

func getSpansWithTagName(spans []mocktracer.Span, tag string) []mocktracer.Span {
	var result []mocktracer.Span
	for _, span := range spans {
		if span.Tag(tag) != nil {
			result = append(result, span)
		}
	}

	return result
}

func getSpansWithTagNameAndValue(spans []mocktracer.Span, tag, value string) []mocktracer.Span {
	var result []mocktracer.Span
	for _, span := range spans {
		if span.Tag(tag) == value {
			result = append(result, span)
		}
	}

	return result
}

func showResourcesNameFromSpans(spans []mocktracer.Span) {
	for i, span := range spans {
		fmt.Printf("  [%d] = %v\n", i, span.Tag(ext.ResourceName))
	}
}
