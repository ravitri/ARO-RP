# Deploy development RP

## Prerequisites

1. Install [Go 1.14](https://golang.org/dl) or later, if you haven't already.

1. Install [Python 3.6+](https://www.python.org/downloads), if you haven't
   already.  You will also need setuptools installed, if you don't have it
   installed already.

1. Install `virtualenv`, a tool for managing Python virtual environments. The
   package is called `python-virtualenv` on both Fedora and Debian-based
   systems.

1. Fedora users: install the `gpgme-devel`, `libassuan-devel`, and `openssl` packages.

   Debian users: install the `libgpgme-dev` package.

   OSX users: please follow [Prepare your development environment using
   OSX](./prepare-your-development-environment-using-osx.md).

   > __NOTE:__ The `gpgme-devel` and `libassuan-devel` libraries are needed in the aro `mirror` functionality.  The dependency can be excluded if one uses build tag `containers_image_openpgp` when building the RP.  See [this link](https://github.com/containers/image#supported-build-tags) for more details on removal.

1. Install the [az
   client](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli), if you
   haven't already. You will need `az` version 2.0.72 or greater, as this
   version includes the `az network vnet subnet update
   --disable-private-link-service-network-policies` flag.

1. Install [OpenVPN](https://openvpn.net/community-downloads), if you haven't
   already.


## Getting started

1. Log in to Azure:

   ```bash
   az login
   ```

1. Git clone this repository to your local machine:

   ```bash
   go get -u github.com/Azure/ARO-RP/...
   cd ${GOPATH:-$HOME/go}/src/github.com/Azure/ARO-RP
   ```


## Installing the extension

1. Build the development `az aro` extension:

   `make az`

1. Verify that the ARO extension path is in your `az` configuration:

   ```bash
   grep -q 'dev_sources' ~/.azure/config || cat >>~/.azure/config <<EOF
   [extension]
   dev_sources = $PWD/python
   EOF
   ```

1. Verify the ARO extension is registered:

   ```bash
   az -v
   ...
   Extensions:
   aro                                0.4.0 (dev) /path/to/rp/python/az/aro
   ...
   Development extension sources:
       /path/to/rp/python
   ...
   ```

   Note: you will be able to update your development `az aro` extension in the
   future by simply running `git pull`.


## Prepare your environment

1. If you don't have access to a shared development environment and secrets,
   follow [prepare a shared RP development
   environment](prepare-a-shared-rp-development-environment.md).

1. Set SECRET_SA_ACCOUNT_NAME to the name of the storage account containing your
   shared development environment secrets and save them in `secrets`:

   ```bash
   SECRET_SA_ACCOUNT_NAME=rharosecrets make secrets
   ```

1. Copy, edit (if necessary) and source your environment file.  The required
   environment variable configuration is documented immediately below:

   ```bash
   cp env.example env
   vi env
   . ./env
   ```

   * `LOCATION`: Location of the shared RP development environment (default:
     `eastus`).
   * `RP_MODE`: Set to `development` to use a development RP running at
     https://localhost:8443/.

1. Create your own RP database:

   ```bash
   az deployment group create \
     -g "$RESOURCEGROUP" \
     -n "databases-development-$USER" \
     --template-file deploy/databases-development.json \
     --parameters \
       "databaseAccountName=$DATABASE_ACCOUNT_NAME" \
       "databaseName=$DATABASE_NAME" \
     >/dev/null
   ```


## Run the RP and create a cluster

1. Source your environment file.

   ```bash
   . ./env
   ```

1. Run the RP

   ```bash
   go run ./cmd/aro rp
   ```

1. To create a cluster, EITHER follow the instructions in [Create, access, and
   manage an Azure Red Hat OpenShift 4.3 Cluster][1].  Note that as long as the
   `RP_MODE` environment variable is set to `development`, the `az aro` client
   will connect to your local RP.

   OR use the create utility:

   ```bash
   CLUSTER=cluster go run ./hack/cluster create
   ```

   Later the cluster can be deleted as follows:

   ```bash
   CLUSTER=cluster go run ./hack/cluster delete
   ```

   [1]: https://docs.microsoft.com/en-us/azure/openshift/tutorial-create-cluster

1. The following additional RP endpoints are available but not exposed via `az
   aro`:

   * Delete a subscription, cascading deletion to all its clusters:

     ```bash
     curl -k -X PUT \
       -H 'Content-Type: application/json' \
       -d '{"state": "Deleted", "properties": {"tenantId": "'"$AZURE_TENANT_ID"'"}}' \
       "https://localhost:8443/subscriptions/$AZURE_SUBSCRIPTION_ID?api-version=2.0"
     ```

   * List operations:

     ```bash
     curl -k \
       "https://localhost:8443/providers/Microsoft.RedHatOpenShift/operations?api-version=2020-04-30"
     ```

   * View RP logs in a friendly format:

     ```bash
     journalctl _COMM=aro -o json --since "15 min ago" -f | jq -r 'select (.COMPONENT != null and (.COMPONENT | contains("access"))|not) | .MESSAGE'
     ```

## Debugging

* SSH to the bootstrap node:
> __NOTE:__ If you have a password-based `sudo` command, you must first authenticate before running `sudo` in the background
  ```bash
  sudo openvpn secrets/vpn-$LOCATION.ovpn &
  CLUSTER=cluster hack/ssh-agent.sh bootstrap
  ```

* Get an admin kubeconfig:

  ```bash
  CLUSTER=cluster make admin.kubeconfig
  export KUBECONFIG=admin.kubeconfig
  ```

* "SSH" to a cluster node:

  * Get the admin kubeconfig and `export KUBECONFIG` as detailed above.
  * Run the ssh-agent.sh script. This takes the argument is the name of the NIC attached to the VM you are trying to ssh to.
   * Given the following nodes these commands would be used to connect to the respective node

    ```
   $ oc get nodes
   NAME                                     STATUS     ROLES    AGE   VERSION
   aro-dev-abc123-master-0               Ready      master   47h   v1.19.0+2f3101c
   aro-dev-abc123-master-1               Ready      master   47h   v1.19.0+2f3101c
   aro-dev-abc123-master-2               Ready      master   47h   v1.19.0+2f3101c
   aro-dev-abc123-worker-eastus1-2s5rb   Ready      worker   47h   v1.19.0+2f3101c
   aro-dev-abc123-worker-eastus2-php82   Ready      worker   47h   v1.19.0+2f3101c
   aro-dev-abc123-worker-eastus3-cbqs2   Ready      worker   47h   v1.19.0+2f3101c


   CLUSTER=cluster hack/ssh-agent.sh master0 # master node aro-dev-abc123-master-0
   CLUSTER=cluster hack/ssh-agent.sh aro-dev-abc123-worker-eastus1-2s5rb # worker aro-dev-abc123-worker-eastus1-2s5rb
   CLUSTER=cluster hack/ssh-agent.sh eastus1 # worker aro-dev-abc123-worker-eastus1-2s5rb
   CLUSTER=cluster hack/ssh-agent.sh 2s5rb  # worker aro-dev-abc123-worker-eastus1-2s5rb
   CLUSTER=cluster hack/ssh-agent.sh bootstrap # the bootstrap node used to provision cluster
   ```

### Metrics

To run fake metrics socket:
```bash
go run ./hack/monitor
```
