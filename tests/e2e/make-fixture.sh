#!/usr/bin/env bash
# Builds the plan-neutral fixture repository used by smoke.sh.
#
# Prints BASE_SHA, HEAD_SHA and DIR as shell assignments so the caller can eval
# them. See README.md for the neutrality contract: this fixture's expected counts
# must not shift when impact-matching behaviour changes.
set -euo pipefail

DIR="$(mktemp -d)/fixture"
mkdir -p "$DIR"
cd "$DIR"

git init -q -b main .
git config user.email "e2e@example.com"
git config user.name "e2e"

# A base and an overlay that consumes it.
mkdir -p apps/base apps/overlay apps/obsolete
cat > apps/base/kustomization.yaml <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
EOF
cat > apps/base/deployment.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  replicas: 1
  selector:
    matchLabels: {app: a}
  template:
    metadata:
      labels: {app: a}
    spec:
      containers:
        - name: c
          image: nginx
EOF
cat > apps/overlay/kustomization.yaml <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../base
EOF
# A directory NOTHING references, so deleting it stays a pure skip whatever
# impact matching decides. Referencing it would make the counts move when the
# deleted-base behaviour changes.
cat > apps/obsolete/kustomization.yaml <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - cm.yaml
EOF
cat > apps/obsolete/cm.yaml <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: obsolete
EOF

git add -A
git commit -qm "fixture base"
BASE_SHA="$(git rev-parse HEAD)"

# The change: edit a referenced file, and delete the unreferenced directory.
sed -i.bak 's/replicas: 1/replicas: 3/' apps/base/deployment.yaml && rm -f apps/base/deployment.yaml.bak
git rm -rq apps/obsolete
git add -A
git commit -qm "fixture change"
HEAD_SHA="$(git rev-parse HEAD)"

# The container runs as UID 1001; the host user is someone else.
chmod -R 0777 "$DIR"

echo "DIR=$DIR"
echo "BASE_SHA=$BASE_SHA"
echo "HEAD_SHA=$HEAD_SHA"
