package config

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// LabelSelector is a Kubernetes label selector (metav1.LabelSelector) in
// muster's YAML configuration, which yaml.v3 reads by these tags rather than
// by the json tags of the Kubernetes type.
type LabelSelector struct {
	MatchLabels      map[string]string          `yaml:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `yaml:"matchExpressions,omitempty"`
}

// LabelSelectorRequirement is one entry of LabelSelector.MatchExpressions.
type LabelSelectorRequirement struct {
	Key      string   `yaml:"key"`
	Operator string   `yaml:"operator"`
	Values   []string `yaml:"values,omitempty"`
}

// Selector converts s with metav1.LabelSelectorAsSelector. A nil s gives a
// nil selector; an empty one matches everything.
func (s *LabelSelector) Selector() (labels.Selector, error) {
	if s == nil {
		return nil, nil
	}
	selector := &metav1.LabelSelector{MatchLabels: s.MatchLabels}
	for _, r := range s.MatchExpressions {
		selector.MatchExpressions = append(selector.MatchExpressions, metav1.LabelSelectorRequirement{
			Key:      r.Key,
			Operator: metav1.LabelSelectorOperator(r.Operator),
			Values:   r.Values,
		})
	}
	converted, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("invalid label selector: %w", err)
	}
	return converted, nil
}
