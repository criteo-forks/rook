/*
Copyright 2019 The Rook Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mon

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/rook/rook/pkg/clusterd"
	cephclient "github.com/rook/rook/pkg/daemon/ceph/client"
	"github.com/rook/rook/pkg/operator/test"
	exectest "github.com/rook/rook/pkg/util/exec/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateClusterSecrets(t *testing.T) {
	ctx := context.TODO()
	clientset := test.New(t, 1)
	configDir := "ns"
	err := os.MkdirAll(configDir, 0755)
	assert.NoError(t, err)
	defer os.RemoveAll(configDir)
	adminSecret := "AQDkLIBd9vLGJxAAnXsIKPrwvUXAmY+D1g0X1Q==" //nolint:gosec // This is just a var name, not a real secret
	executor := &exectest.MockExecutor{
		MockExecuteCommandWithOutput: func(command string, args ...string) (string, error) {
			logger.Infof("COMMAND: %s %v", command, args)
			if command == "ceph-authtool" && args[0] == "--create-keyring" {
				filename := args[1]
				assert.NoError(t, ioutil.WriteFile(filename, []byte(fmt.Sprintf("key = %s", adminSecret)), 0600))
			}
			return "", nil
		},
	}
	context := &clusterd.Context{
		Clientset: clientset,
		Executor:  executor,
	}
	monSpec := cephv1.MonSpec{
		Msgr1Port: 6789,
		Msgr2Port: 3300,
	}
	namespace := "ns"
	ownerInfo := cephclient.NewMinimumOwnerInfoWithOwnerRef()
	info, maxID, mapping, err := CreateOrLoadClusterInfo(context, namespace, &monSpec, ownerInfo)
	assert.NoError(t, err)
	assert.Equal(t, -1, maxID)
	require.NotNil(t, info)
	assert.Equal(t, "client.admin", info.CephCred.Username)
	assert.Equal(t, adminSecret, info.CephCred.Secret)
	assert.NotEqual(t, "", info.FSID)
	assert.NotNil(t, mapping)

	// check for the cluster secret
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "rook-ceph-mon", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, adminSecret, string(secret.Data["ceph-secret"]))

	// For backward compatibility check that the admin secret can be loaded as previously specified
	// Update the secret as if created in an old cluster
	delete(secret.Data, cephUserSecretKey)
	delete(secret.Data, cephUsernameKey)
	secret.Data[adminSecretNameKey] = []byte(adminSecret)
	_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	assert.NoError(t, err)

	// Check that the cluster info can now be loaded
	info, _, _, err = CreateOrLoadClusterInfo(context, namespace, &monSpec, ownerInfo)
	assert.NoError(t, err)
	assert.Equal(t, "client.admin", info.CephCred.Username)
	assert.Equal(t, adminSecret, info.CephCred.Secret)

	// Fail to load the external cluster if the admin placeholder is specified
	secret.Data[adminSecretNameKey] = []byte(adminSecretNameKey)
	_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	assert.NoError(t, err)
	_, _, _, err = CreateOrLoadClusterInfo(context, namespace, &monSpec, ownerInfo)
	assert.Error(t, err)

	// Load the external cluster with the legacy external creds
	secret.Name = OperatorCreds
	secret.Data = map[string][]byte{
		"userID":  []byte("testid"),
		"userKey": []byte("testkey"),
	}
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	assert.NoError(t, err)
	info, _, _, err = CreateOrLoadClusterInfo(context, namespace, &monSpec, ownerInfo)
	assert.NoError(t, err)
	assert.Equal(t, "testid", info.CephCred.Username)
	assert.Equal(t, "testkey", info.CephCred.Secret)
}
