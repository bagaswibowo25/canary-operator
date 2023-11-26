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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// CanaryReleaseReconciler untuk melakukan reconciles pada objek CanaryRelease
type CanaryReleaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Fungsi utama untuk melakukan reconcile objek CanaryRelease
func (r *CanaryReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	// 1. Ambil instance CanaryRelease dari cluster
	cr := &releasev1.CanaryRelease{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		log.Error(err, "Canary resource not found!", "CanaryRelease", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.Info("Info: Got canary resource " + cr.Name)

	// 2. Ambil instance Deployment utama dari cluster
	appDeployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Spec.DeploymentName, Namespace: cr.Namespace}, appDeployment); err != nil {
		log.Info("Warning: Deployment is not exist")
	}
	log.Info("Info: Primary deployment is: "+ cr.Spec.DeploymentName)

	// 3. Cek resource canary deployment
	canaryDeploymentName := cr.Spec.DeploymentName + "-canary"
	canaryDeployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: canaryDeploymentName, Namespace: req.Namespace}, canaryDeployment)

	// 4. Buat canary deployment jika tidak ada
	if err != nil && apierrors.IsNotFound(err) {
	    canaryDeployment = createCanaryDeployment(cr, appDeployment, canaryDeploymentName)
		if err := r.Create(ctx, canaryDeployment); err != nil {
			log.Error(err, "Error: Failed to create canary deployment", "CanaryRelease", req.NamespacedName)
		}
		log.Info("Info: Failed to get canary deployment resource, canary deployment will be created")
	} else if err != nil {
		log.Error(err, "Warning: Failed to get canary deployment", "Deployment", req.NamespacedName)
	}

	// 5. Sesuaikan jumlah replica Deployment berdasarkan CanaryRelease
	if err := r.adjustDeploymentReplicas(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Info: Adjusting deployment replica")

	// 6. Melakukan pengecekan dan jalankan rollout atau rollback
	if cr.Spec.PerformRollout {
		if err := r.rollout(ctx, cr, appDeployment); err != nil {
		    log.Error(err, "Failed to perform rollout")
		    return ctrl.Result{}, err
		}
		// 7. Reset PerformRollout field
		cr.Spec.PerformRollout = false
		if err := r.Update(ctx, cr); err != nil {
		    log.Error(err, "Failed to update CanaryRelease after rollout")
		}
	}
	else if cr.Spec.PerformRollback {
		if err := r.rollback(ctx, cr, appDeployment); err != nil {
			log.Error(err, "Failed to perform rollback")
			return ctrl.Result{}, err
	    }
        // 8. Reset PerformRollback field
        cr.Spec.PerformRollback = false
        if err := r.Update(ctx, cr); err != nil {
            log.Error(err, "Failed to update CanaryRelease after rollback")
        }
	}

	return ctrl.Result{}, nil
}

// Fungsi untuk membuat deployment canary

func createCanaryDeployment(cr *releasev1.CanaryRelease, appDeployment *appsv1.Deployment, name string) *appsv1.Deployment {
    // 1. Copy deployment dari deployment utama
    canaryDeployment := appDeployment.DeepCopy()
    var canaryLabel = map[string]string{"version":"canary","app":cr.Spec.DeploymentName}
    canaryDeployment.ObjectMeta = metav1.ObjectMeta{
        Name:      name,
        Namespace: cr.Namespace,
	Labels:    canaryLabel,
    }

    // 2. Sesuaikan spesifikasi deployment canary sesuai kebutuhan
    canaryDeployment.Spec.Template.Spec.Containers[0].Image = cr.Spec.CanaryImage
    canaryDeployment.Spec.Selector.MatchLabels = canaryLabel
    canaryDeployment.Spec.Template.ObjectMeta = metav1.ObjectMeta{
        Labels: canaryLabel,
    }

    return canaryDeployment
}

// Fungsi untuk mengatur jumlah replica untuk deployment utama dan deployment canary

func (r *CanaryReleaseReconciler) adjustDeploymentReplicas(ctx context.Context, cr *releasev1.CanaryRelease) error {
	// 1. Menghitung jumlah replika berdasarkan splitPercentage
	replicasPrimary := int32(math.Round(float64(cr.Spec.TotalReplicas) * (float64(cr.Spec.SplitPercentage) / 100.0)))
	replicasCanary := int32(cr.Spec.TotalReplicas) - replicasPrimary

	// 2. Mengambil dan memperbarui Deployment Primary
	deploymentPrimary := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Spec.DeploymentName, Namespace: cr.Namespace}, deploymentPrimary); err != nil {
		return err
	}
	deploymentPrimary.Spec.Replicas = &replicasPrimary
	if err := r.Update(ctx, deploymentPrimary); err != nil {
		return err
	}

	// 3. Mengambil dan memperbarui Deployment Canary
	deploymentCanary := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Spec.DeploymentName + "-canary", Namespace: cr.Namespace}, deploymentCanary); err != nil {
		return err
	}
	deploymentCanary.Spec.Replicas = &replicasCanary
	if err := r.Update(ctx, deploymentCanary); err != nil {
		return err
	}

	return nil
}

// Fungsi untuk melakukan rollout pada deployment

func (r *CanaryReleaseReconciler) rollout(ctx context.Context, cr *releasev1.CanaryRelease, mainDeployment *appsv1.Deployment) error {
    // 1.  Ganti image deployment utama dengan image canary
    mainDeployment.Spec.Template.Spec.Containers[0].Image = cr.Spec.CanaryImage
    if err := r.Update(ctx, mainDeployment); err != nil {
        return err
    }

    // 2. Set splitPercentage menjadi 100 & update image utama
    cr.Spec.SplitPercentage = 100
    cr.Spec.MainImage = cr.Spec.CanaryImage
    if err := r.Update(ctx, cr); err != nil {
        return err
    }

    return nil
}

// Fungsi untuk melakukan rollback pada deployment

func (r *CanaryReleaseReconciler) rollback(ctx context.Context, cr *releasev1.CanaryRelease, mainDeployment *appsv1.Deployment) error {
    // 1. Ganti image deployment utama dengan image existing
    mainDeployment.Spec.Template.Spec.Containers[0].Image = cr.Spec.MainImage
    if err := r.Update(ctx, mainDeployment); err != nil {
        return err
    }

    // 2. Set splitPercentage menjadi 100
    cr.Spec.SplitPercentage = 100
    if err := r.Update(ctx, cr); err != nil {
        return err
    }

	return nil
}

func (r *CanaryReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&releasev1.CanaryRelease{}).
		Complete(r)
}
