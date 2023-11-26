/*
Copyright 2023.

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

package controllers

import (
	appsv1 "k8s.io/api/apps/v1"
	"math"
	"k8s.io/apimachinery/pkg/runtime"
	releasev1 "github.com/bagaswibowo25/canary-operator/api/v1"
	"k8s.io/apimachinery/pkg/types"
	"context"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CanaryReleaseReconciler reconciles a CanaryRelease object
type CanaryReleaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *CanaryReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	// 1. Ambil instance CanaryRelease dari cluster
	cr := &releasev1.CanaryRelease{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Ambil instance Deployment dari cluster
	appDeployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamescpacedName{Name: cr.Spec.DeploymentName, Namespace: cr.Namespace}, appDeployment); err != nil {
		log.Info("Warning: Deployment is not exist")
	}
	// 3. Cek resource canary deployment
	canaryDeploymentName := cr.Spec.DeploymentPrimary + "-canary"
	canaryDeployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: canaryDeploymentName, Namespace: req.Namespace}, canaryDeployment)
	// 4. Buat canary deployment jika tidak ada
	if err != nil && errors.IsNotFound(err) {
	    canaryDeployment = createCanaryDeployment(cr, appDeployment, canaryDeploymentName)
		if err := r.Create(ctx, &canaryDeployment); err != nil {
				log.Info("Error: Failed to create canary deployment")
		}
		log.Info("Warning: Canary deployment is not exist or failed to create")
	} else if err != nil {
		log.Info("Warning: Failed to get canary deployment")
	}

	// 4. Sesuaikan jumlah replika Deployment berdasarkan CanaryRelease
	if err := r.adjustDeploymentReplicas(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// Fungsi untuk membuat deployment canary

func createCanaryDeployment(cr releasev1.CanaryRelease, appDeployment appsv1.Deployment, name string) appsv1.Deployment {
    // Copy deployment dari deployment utama
    canaryDeployment := appDeployment.DeepCopy()
    var canaryLabel = map[string]string{"app":"canary"}
    canaryDeployment.ObjectMeta = metav1.ObjectMeta{
        Name:      name,
        Namespace: cr.Namespace,
	Labels:    canaryLabel,
    }

    // Sesuaikan spesifikasi deployment canary sesuai kebutuhan
    canaryDeployment.Spec.Template.Spec.Containers[0].Image = cr.Spec.CanaryImage

    return *canaryDeployment
}

func (r *CanaryReleaseReconciler) adjustDeploymentReplicas(ctx context.Context, cr *releasev1.CanaryRelease) error {
	// Menghitung jumlah replika berdasarkan splitPercentage
	replicasPrimary := int32(math.Round(float64(cr.Spec.TotalReplicas) * (float64(cr.Spec.SplitPercentage) / 100.0)))
	replicasCanary := int32(cr.Spec.TotalReplicas) - replicasPrimary

	// Mengambil dan memperbarui Deployment Primary
	deploymentPrimary := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Spec.DeploymentPrimary, Namespace: cr.Namespace}, deploymentPrimary); err != nil {
		return err
	}
	deploymentPrimary.Spec.Replicas = &replicasPrimary
	if err := r.Update(ctx, deploymentPrimary); err != nil {
		return err
	}

	// Mengambil dan memperbarui Deployment Canary
	deploymentCanary := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Spec.DeploymentCanary, Namespace: cr.Namespace}, deploymentCanary); err != nil {
		return err
	}
	deploymentCanary.Spec.Replicas = &replicasCanary
	if err := r.Update(ctx, deploymentCanary); err != nil {
		return err
	}

	return nil
}

func (r *CanaryReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&releasev1.CanaryRelease{}).
		Complete(r)
}
