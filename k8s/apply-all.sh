#!/bin/bash

# Kubernetes deployment script for EchoFlow
# Applies all manifests in the correct order

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Deploying EchoFlow to namespace: echoflow ==="
echo ""

# Check kubectl connection
echo "Checking kubectl connection..."
if ! kubectl cluster-info > /dev/null 2>&1; then
    echo "Error: Cannot connect to Kubernetes cluster"
    exit 1
fi

# 1. Namespace (with restricted PSS)
echo "[1/10] Applying namespace..."
kubectl apply -f "$SCRIPT_DIR/namespace.yaml"

# 2. RBAC (ServiceAccount, Role, RoleBinding)
echo "[2/10] Applying RBAC..."
kubectl apply -f "$SCRIPT_DIR/rbac.yaml"

# 3. NetworkPolicy
echo "[3/10] Applying network policies..."
kubectl apply -f "$SCRIPT_DIR/network-policy.yaml"

# 4. ConfigMap
echo "[4/10] Applying configmap..."
kubectl apply -f "$SCRIPT_DIR/configmap.yaml"

# 5. Deployment
echo "[5/10] Applying deployment..."
kubectl apply -f "$SCRIPT_DIR/deployment.yaml"

# 6. Service
echo "[6/10] Applying service..."
kubectl apply -f "$SCRIPT_DIR/service.yaml"

# 7. Ingress
echo "[7/10] Applying ingress..."
kubectl apply -f "$SCRIPT_DIR/ingress.yaml"

# 8. Certificate
echo "[8/10] Applying TLS certificate..."
kubectl apply -f "$SCRIPT_DIR/certificate.yaml"

# 9. HPA
echo "[9/10] Applying HPA..."
kubectl apply -f "$SCRIPT_DIR/hpa.yaml"

# 10. PDB
echo "[10/10] Applying PDB..."
kubectl apply -f "$SCRIPT_DIR/pdb.yaml"

echo ""
echo "=== Deployment complete ==="
echo ""

# Show status
echo "Current status:"
echo "---------------"
kubectl get pods -n echoflow -l app=echoflow
echo ""
kubectl get hpa -n echoflow
echo ""
kubectl get pdb -n echoflow
