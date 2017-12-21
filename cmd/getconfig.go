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
	"errors"
	"fmt"
	"os"

	"github.com/kris-nova/kubicorn/cutil/agent"
	"github.com/kris-nova/kubicorn/cutil/initapi"
	"github.com/kris-nova/kubicorn/cutil/kubeconfig"
	"github.com/kris-nova/kubicorn/cutil/logger"
	"github.com/kris-nova/kubicorn/state"
	"github.com/kris-nova/kubicorn/state/fs"
	"github.com/kris-nova/kubicorn/state/git"
	"github.com/kris-nova/kubicorn/state/jsonfs"
	"github.com/kris-nova/kubicorn/state/s3"
	"github.com/minio/minio-go"
	"github.com/spf13/cobra"
	gg "github.com/tcnksm/go-gitconfig"
)

type GetConfigOptions struct {
	Options
}

var cro = &GetConfigOptions{}

// GetConfigCmd represents the apply command
func GetConfigCmd() *cobra.Command {
	var getConfigCmd = &cobra.Command{
		Use:   "getconfig <NAME>",
		Short: "Manage Kubernetes configuration",
		Long: `Use this command to pull a kubeconfig file from a cluster so you can use kubectl.
	
	This command will attempt to find a cluster, and append a local kubeconfig file with a kubeconfig `,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				cro.Name = strEnvDef("KUBICORN_NAME", "")
			} else if len(args) > 1 {
				logger.Critical("Too many arguments.")
				os.Exit(1)
			} else {
				cro.Name = args[0]
			}

			err := RunGetConfig(cro)
			if err != nil {
				logger.Critical(err.Error())
				os.Exit(1)
			}

		},
	}

	getConfigCmd.Flags().StringVarP(&cro.StateStore, "state-store", "s", strEnvDef("KUBICORN_STATE_STORE", "fs"), "The state store type to use for the cluster")
	getConfigCmd.Flags().StringVarP(&cro.StateStorePath, "state-store-path", "S", strEnvDef("KUBICORN_STATE_STORE_PATH", "./_state"), "The state store path to use")

	// git flags
	getConfigCmd.Flags().StringVar(&cro.GitRemote, "git-config", strEnvDef("KUBICORN_GIT_CONFIG", "git"), "The git remote url to use")

	// s3 flags
	getConfigCmd.Flags().StringVar(&cro.S3AccessKey, "s3-access", strEnvDef("KUBICORN_S3_ACCESS_KEY", ""), "The s3 access key.")
	getConfigCmd.Flags().StringVar(&cro.S3SecretKey, "s3-secret", strEnvDef("KUBICORN_S3_SECRET_KEY", ""), "The s3 secret key.")
	getConfigCmd.Flags().StringVar(&cro.BucketEndpointURL, "s3-endpoint", strEnvDef("KUBICORN_S3_ENDPOINT", ""), "The s3 endpoint url.")
	getConfigCmd.Flags().StringVar(&cro.BucketLocation, "s3-location", strEnvDef("KUBICORN_S3_LOCATION", ""), "The s3 bucket location.")
	getConfigCmd.Flags().StringVar(&cro.BucketName, "s3-bucket", strEnvDef("KUBICORN_S3_BUCKET", ""), "The s3 bucket name to be used for saving the s3 state for the cluster.")

	return getConfigCmd
}

func RunGetConfig(options *GetConfigOptions) error {

	// Ensure we have SSH agent
	agent := agent.NewAgent()

	// Ensure we have a name
	name := options.Name
	if name == "" {
		return errors.New("Empty name. Must specify the name of the cluster to get config")
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
		client, err := minio.New(cro.BucketEndpointURL, cro.S3AccessKey, cro.S3SecretKey, true)
		if err != nil {
			return err
		}

		logger.Info("Selected [s3] state store")
		stateStore = s3.NewJSONFS3Store(&s3.JSONS3StoreOptions{
			Client:      client,
			BasePath:    options.StateStorePath,
			ClusterName: name,
			BucketOptions: &s3.S3BucketOptions{
				EndpointURL:    cro.BucketEndpointURL,
				BucketName:     cro.BucketName,
				BucketLocation: cro.BucketLocation,
			},
		})
	}

	cluster, err := stateStore.GetCluster()
	if err != nil {
		return fmt.Errorf("Unable to get cluster [%s]: %v", name, err)
	}
	logger.Info("Loaded cluster: %s", cluster.Name)

	cluster, err = initapi.InitCluster(cluster)
	if err != nil {
		return err
	}

	err = kubeconfig.GetConfig(cluster, agent)
	if err != nil {
		return err
	}
	logger.Always("Applied kubeconfig")

	return nil
}
