# ------------------------------------------------------------------------------------------------------------------------
# We are explicitly not using a templating language to inject the values as to encourage the user to limit their
# use of templating logic in these files. By design all injected values should be able to be set at runtime,
# and the shell script real work. If you need conditional logic, write it in bash or make another shell script.
# ------------------------------------------------------------------------------------------------------------------------

apt-get update -y
apt-get install -y jq

K8SVER=$(cat /etc/kubicorn/cluster.json | jq -r '.values.itemMap.KUBERNETESVERSION')
CNIVER=$(cat /etc/kubicorn/cluster.json | jq -r '.values.itemMap.INJECTEDTOKEN')

curl -s https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo apt-key add -
touch /etc/apt/sources.list.d/kubernetes.list
sh -c 'echo "deb http://apt.kubernetes.io/ kubernetes-xenial main" > /etc/apt/sources.list.d/kubernetes.list'

apt-get update -y
apt-get install -y \
    socat \
    ebtables \
    docker.io \
    apt-transport-https \
    kubelet \
    kubeadm=1.7.0-00 \
    cloud-utils \
    jq


systemctl enable docker
systemctl start docker

PUBLICIP=$(curl -s http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address)
VPNIP=$(ip addr show dev tun0 | awk '/inet / {print $2}' | cut -d"/" -f1)
echo $VPNIP > /tmp/.ip

TOKEN=$(cat /etc/kubicorn/cluster.json | jq -r '.values.itemMap.INJECTEDTOKEN')
PORT=$(cat /etc/kubicorn/cluster.json | jq -r '.values.itemMap.INJECTEDPORT | tonumber')

kubeadm reset
kubeadm init --apiserver-bind-port ${PORT} --token ${TOKEN}  --apiserver-advertise-address ${PUBLICIP} --apiserver-cert-extra-sans ${PUBLICIP} ${VPNIP}


kubectl apply \
  -f http://docs.projectcalico.org/v2.3/getting-started/kubernetes/installation/hosted/kubeadm/1.6/calico.yaml \
  --kubeconfig /etc/kubernetes/admin.conf

# Root
mkdir -p ~/.kube
cp /etc/kubernetes/admin.conf ~/.kube/config

