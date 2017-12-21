// Copyright © 2017 The Kubicorn Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"
	"os"

	"github.com/kris-nova/kubicorn/apis/cluster"
	"github.com/kris-nova/kubicorn/cutil"
	"github.com/kris-nova/kubicorn/cutil/logger"
	"github.com/kris-nova/kubicorn/cutil/task"
	"github.com/kris-nova/kubicorn/state"
	"github.com/kris-nova/kubicorn/state/fs"
	"github.com/kris-nova/kubicorn/state/git"
	"github.com/kris-nova/kubicorn/state/jsonfs"
	"github.com/kris-nova/kubicorn/state/s3"
	"github.com/minio/minio-go"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	gg "github.com/tcnksm/go-gitconfig"
)

type DeleteOptions struct {
	Options
	Purge bool
}

var do = &DeleteOptions{}

// DeleteCmd represents the delete command
func DeleteCmd() *cobra.Command {
	var deleteCmd = &cobra.Command{
		Use:   "delete <NAME>",
		Short: "Delete a Kubernetes cluster",
		Long: `Use this command to delete cloud resources.
	
	This command will attempt to build the resource graph based on an API model.
	Once the graph is built, the delete will attempt to delete the resources from the cloud.
	After the delete is complete, the state store will be left in tact and could potentially be applied later.
	
	To delete the resource AND the API model in the state store, use --purge.`,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				do.Name = strEnvDef("KUBICORN_NAME", "")
			} else if len(args) > 1 {
				logger.Critical("Too many arguments.")
				os.Exit(1)
			} else {
				do.Name = args[0]
			}

			err := RunDelete(do)
			if err != nil {
				logger.Critical(err.Error())
				os.Exit(1)
			}

		},
	}

	deleteCmd.Flags().StringVarP(&do.StateStore, "state-store", "s", strEnvDef("KUBICORN_STATE_STORE", "fs"), "The state store type to use for the cluster")
	deleteCmd.Flags().StringVarP(&do.StateStorePath, "state-store-path", "S", strEnvDef("KUBICORN_STATE_STORE_PATH", "./_state"), "The state store path to use")
	deleteCmd.Flags().BoolVarP(&do.Purge, "purge", "p", false, "Remove the API model from the state store after the resources are deleted.")
	deleteCmd.Flags().StringVar(&do.AwsProfile, "aws-profile", strEnvDef("KUBICORN_AWS_PROFILE", ""), "The profile to be used as defined in $HOME/.aws/credentials")

	// git flags
	deleteCmd.Flags().StringVar(&do.GitRemote, "git-config", strEnvDef("KUBICORN_GIT_CONFIG", "git"), "The git remote url to use")

	// s3 flags
	deleteCmd.Flags().StringVar(&do.S3AccessKey, "s3-access", strEnvDef("KUBICORN_S3_ACCESS_KEY", ""), "The s3 access key.")
	deleteCmd.Flags().StringVar(&do.S3SecretKey, "s3-secret", strEnvDef("KUBICORN_S3_SECRET_KEY", ""), "The s3 secret key.")
	deleteCmd.Flags().StringVar(&do.BucketEndpointURL, "s3-endpoint", strEnvDef("KUBICORN_S3_ENDPOINT", ""), "The s3 endpoint url.")
	deleteCmd.Flags().BoolVar(&do.BucketSSL, "s3-ssl", boolEnvDef("KUBICORN_S3_BUCKET", true), "The s3 bucket name to be used for saving the git state for the cluster.")
	deleteCmd.Flags().StringVar(&do.BucketName, "s3-bucket", strEnvDef("KUBICORN_S3_BUCKET", ""), "The s3 bucket name to be used for saving the s3 state for the cluster.")

	return deleteCmd
}

func RunDelete(options *DeleteOptions) error {

	// Ensure we have a name
	name := options.Name
	if name == "" {
		return errors.New("Empty name. Must specify the name of the cluster to delete")
	}
	// Expand state store path
	options.StateStorePath = expandPath(options.StateStorePath)

	// Register state store
	var stateStore state.ClusterStorer
	switch options.StateStore {
	case "fs":
		logger.Info("Selected [fs] state store")
		stateStore = fs.NewFileSystemStore(&fs.FileSystemStoreOptions{
			BasePath:    options.StateStorePath,
			ClusterName: name,
		})
	case "git":
		logger.Info("Selected [git] state store")
		if options.GitRemote == "" {
			return errors.New("Empty GitRemote url. Must specify the link to the remote git repo.")
		}
		user, _ := gg.Global("user.name")
		email, _ := gg.Email()

		stateStore = git.NewJSONGitStore(&git.JSONGitStoreOptions{
			BasePath:    options.StateStorePath,
			ClusterName: name,
			CommitConfig: &git.JSONGitCommitConfig{
				Name:   user,
				Email:  email,
				Remote: options.GitRemote,
			},
		})
	case "jsonfs":
		logger.Info("Selected [jsonfs] state store")
		stateStore = jsonfs.NewJSONFileSystemStore(&jsonfs.JSONFileSystemStoreOptions{
			BasePath:    options.StateStorePath,
			ClusterName: name,
		})
	case "s3":
		client, err := minio.New(do.BucketEndpointURL, do.S3AccessKey, do.S3SecretKey, do.BucketSSL)
		if err != nil {
			return err
		}

		logger.Info("Selected [s3] state store")
		stateStore = s3.NewJSONFS3Store(&s3.JSONS3StoreOptions{
			Client:      client,
			BasePath:    options.StateStorePath,
			ClusterName: name,
			BucketOptions: &s3.S3BucketOptions{
				EndpointURL:    do.BucketEndpointURL,
				BucketName:     do.BucketName,
			},
		})
	}

	if !stateStore.Exists() {
		logger.Info("Cluster [%s] does not exist", name)
		return nil
	}

	expectedCluster, err := stateStore.GetCluster()
	if err != nil {
		return fmt.Errorf("Unable to get cluster [%s]: %v", name, err)
	}

	runtimeParams := &cutil.RuntimeParameters{}

	if len(do.AwsProfile) > 0 {
		runtimeParams.AwsProfile = do.AwsProfile
	}

	reconciler, err := cutil.GetReconciler(expectedCluster, runtimeParams)
	if err != nil {
		return fmt.Errorf("Unable to get cluster reconciler: %v", err)
	}
	var deleteCluster *cluster.Cluster
	var deleteClusterTask = func() error {
		deleteCluster, err = reconciler.Destroy()
		return err
	}

	err = task.RunAnnotated(deleteClusterTask, fmt.Sprintf("\nDestroying resources for cluster [%s]:\n", options.Name), "")
	if err != nil {
		return fmt.Errorf("Unable to destroy resources for cluster [%s]: %v", options.Name, err)
	}

	err = stateStore.Commit(deleteCluster)
	if err != nil {
		return fmt.Errorf("Unable to save state store: %v", err)
	}

	if options.Purge {
		err := stateStore.Destroy()
		if err != nil {
			return fmt.Errorf("Unable to remove state store for cluster [%s]: %v", options.Name, err)
		}
	}
	return nil
}
