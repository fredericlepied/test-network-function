// Copyright (C) 2021 Red Hat, Inc.
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along
// with this program; if not, write to the Free Software Foundation, Inc.,
// 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

package autodiscover

import (
	"encoding/json"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/test-network-function/test-network-function/pkg/config/configsections"
	"github.com/test-network-function/test-network-function/pkg/tnf"
	"github.com/test-network-function/test-network-function/pkg/tnf/handlers/nodenames"
	"github.com/test-network-function/test-network-function/pkg/tnf/interactive"
	"github.com/test-network-function/test-network-function/pkg/tnf/reel"
	"github.com/test-network-function/test-network-function/pkg/tnf/testcases"
	"github.com/test-network-function/test-network-function/pkg/utils"
)

const (
	operatorLabelName           = "operator"
	skipConnectivityTestsLabel  = "skip_connectivity_tests"
	ocGetClusterCrdNamesCommand = "kubectl get crd -o json | jq '[.items[].metadata.name]'"
	DefaultTimeout              = 10 * time.Second
)

var (
	operatorTestsAnnotationName    = buildAnnotationName("operator_tests")
	subscriptionNameAnnotationName = buildAnnotationName("subscription_name")
	podTestsAnnotationName         = buildAnnotationName("host_resource_tests")
)

// FindTestTarget finds test targets from the current state of the cluster,
// using labels and annotations, and add them to the `configsections.TestTarget` passed in.
func FindTestTarget(labels []configsections.Label, target *configsections.TestTarget, namespace string) {
	// find pods by label
	for _, l := range labels {
		pods, err := GetPodsByLabel(l, namespace)
		if err == nil {
			for i := range pods.Items {
				target.PodsUnderTest = append(target.PodsUnderTest, buildPodUnderTest(pods.Items[i]))
				target.ContainerConfigList = append(target.ContainerConfigList, buildContainersFromPodResource(pods.Items[i])...)
			}
		} else {
			log.Warnf("failed to query by label: %v %v", l, err)
		}
	}
	// Containers to exclude from connectivity tests are optional
	identifiers, err := getContainerIdentifiersByLabel(configsections.Label{Prefix: tnfLabelPrefix, Name: skipConnectivityTestsLabel, Value: anyLabelValue}, namespace)
	target.ExcludeContainersFromConnectivityTests = identifiers

	if err != nil {
		log.Warnf("an error (%s) occurred when getting the containers to exclude from connectivity tests. Attempting to continue", err)
	}

	csvs, err := GetCSVsByLabel(operatorLabelName, anyLabelValue, namespace)
	if err == nil {
		for i := range csvs.Items {
			target.Operators = append(target.Operators, buildOperatorFromCSVResource(&csvs.Items[i]))
		}
	} else {
		log.Warnf("an error (%s) occurred when looking for operaters by label", err)
	}

	target.DeploymentsUnderTest = append(target.DeploymentsUnderTest, FindTestDeployments(labels, target, namespace)...)
	target.Nodes = GetNodesList()
}

// GetNodesList Function that return a list of node and what is the type of them.
func GetNodesList() (nodes map[string]configsections.Node) {
	nodes = make(map[string]configsections.Node)
	var nodeNames []string
	context := interactive.GetContext(expectersVerboseModeEnabled)
	tester := nodenames.NewNodeNames(DefaultTimeout, map[string]*string{configsections.MasterLabel: nil})
	test, _ := tnf.NewTest(context.GetExpecter(), tester, []reel.Handler{tester}, context.GetErrorChannel())
	_, err := test.Run()
	if err != nil {
		log.Error("Unable to get node list ", ". Error: ", err)
		return
	}
	nodeNames = tester.GetNodeNames()
	for i := range nodeNames {
		nodes[nodeNames[i]] = configsections.Node{
			Name:   nodeNames[i],
			Labels: []string{configsections.MasterLabel},
		}
	}

	tester = nodenames.NewNodeNames(DefaultTimeout, map[string]*string{configsections.WorkerLabel: nil})
	test, _ = tnf.NewTest(context.GetExpecter(), tester, []reel.Handler{tester}, context.GetErrorChannel())
	_, err = test.Run()
	if err != nil {
		log.Error("Unable to get node list ", ". Error: ", err)
	} else {
		nodeNames = tester.GetNodeNames()
		for i := range nodeNames {
			if _, ok := nodes[nodeNames[i]]; ok {
				var node = nodes[nodeNames[i]]
				node.Labels = append(node.Labels, configsections.WorkerLabel)
				nodes[nodeNames[i]] = node
			} else {
				nodes[nodeNames[i]] = configsections.Node{
					Name:   nodeNames[i],
					Labels: []string{configsections.WorkerLabel},
				}
			}
		}
	}
	return nodes
}

// FindTestDeployments uses the containers' namespace to get its parent deployment. Filters out non CNF test deployments,
// currently partner and fs_diff ones.
func FindTestDeployments(targetLabels []configsections.Label, target *configsections.TestTarget, namespace string) (deployments []configsections.Deployment) {
	for _, label := range targetLabels {
		deploymentResourceList, err := GetTargetDeploymentsByNamespace(namespace, label)
		if err != nil {
			log.Error("Unable to get deployment list from namespace ", namespace, ". Error: ", err)
		} else {
			for _, deploymentResource := range deploymentResourceList.Items {
				deployment := configsections.Deployment{
					Name:      deploymentResource.GetName(),
					Namespace: deploymentResource.GetNamespace(),
					Replicas:  deploymentResource.GetReplicas(),
				}

				deployments = append(deployments, deployment)
			}
		}
	}
	return deployments
}

// buildPodUnderTest builds a single `configsections.Pod` from a PodResource
func buildPodUnderTest(pr *PodResource) (podUnderTest configsections.Pod) {
	var err error
	podUnderTest.Namespace = pr.Metadata.Namespace
	podUnderTest.Name = pr.Metadata.Name
	podUnderTest.ServiceAccount = pr.Spec.ServiceAccount
	podUnderTest.ContainerCount = len(pr.Spec.Containers)
	var tests []string
	err = pr.GetAnnotationValue(podTestsAnnotationName, &tests)
	if err != nil {
		log.Warnf("unable to extract tests from annotation on '%s/%s' (error: %s). Attempting to fallback to all tests", podUnderTest.Namespace, podUnderTest.Name, err)
		podUnderTest.Tests = testcases.GetConfiguredPodTests()
	} else {
		podUnderTest.Tests = tests
	}
	return
}

// buildOperatorFromCSVResource builds a single `configsections.Operator` from a CSVResource
func buildOperatorFromCSVResource(csv *CSVResource) (op configsections.Operator) {
	var err error
	op.Name = csv.Metadata.Name
	op.Namespace = csv.Metadata.Namespace

	var tests []string
	err = csv.GetAnnotationValue(operatorTestsAnnotationName, &tests)
	if err != nil {
		log.Warnf("unable to extract tests from annotation on '%s/%s' (error: %s). Attempting to fallback to all tests", op.Namespace, op.Name, err)
		op.Tests = getConfiguredOperatorTests()
	} else {
		op.Tests = tests
	}

	var subscriptionName []string
	err = csv.GetAnnotationValue(subscriptionNameAnnotationName, &subscriptionName)
	if err != nil {
		log.Warnf("unable to get a subscription name annotation from CSV %s (error: %s).", csv.Metadata.Name, err)
	} else {
		op.SubscriptionName = subscriptionName[0]
	}
	return op
}

// getConfiguredOperatorTests loads the `configuredTestFile` used by the `operator` specs and extracts
// the names of test groups from it.
func getConfiguredOperatorTests() (opTests []string) {
	configuredTests, err := testcases.LoadConfiguredTestFile(testcases.ConfiguredTestFile)
	if err != nil {
		log.Errorf("failed to load %s, continuing with no tests", testcases.ConfiguredTestFile)
		return []string{}
	}
	for _, configuredTest := range configuredTests.OperatorTest {
		opTests = append(opTests, configuredTest.Name)
	}
	log.WithField("opTests", opTests).Infof("got all tests from %s.", testcases.ConfiguredTestFile)
	return opTests
}

// getClusterCrdNames returns a list of crd names found in the cluster.
func getClusterCrdNames() ([]string, error) {
	out := utils.ExecuteCommand(ocGetClusterCrdNamesCommand, ocCommandTimeOut, interactive.GetContext(expectersVerboseModeEnabled), func() {
		log.Error("can't run command: ", ocGetClusterCrdNamesCommand)
	})

	var crdNamesList []string
	err := json.Unmarshal([]byte(out), &crdNamesList)
	if err != nil {
		return nil, err
	}

	return crdNamesList, nil
}

// FindTestCrdNames gets a list of CRD names based on configured groups.
func FindTestCrdNames(crdFilters []configsections.CrdFilter) []string {
	clusterCrdNames, err := getClusterCrdNames()
	if err != nil {
		log.Errorf("Unable to get cluster CRD.")
		return []string{}
	}

	var targetCrdNames []string
	for _, crdName := range clusterCrdNames {
		for _, crdFilter := range crdFilters {
			if strings.HasSuffix(crdName, crdFilter.NameSuffix) {
				targetCrdNames = append(targetCrdNames, crdName)
				break
			}
		}
	}
	return targetCrdNames
}
