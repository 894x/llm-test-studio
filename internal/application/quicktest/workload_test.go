package quicktest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalPerformanceWorkloadIsDeterministicAndBounded(t *testing.T) {
	profile := PerformanceProfile{
		WorkloadMode:       PerformanceWorkloadNormal,
		RandomSeed:         8675309,
		InputTokens:        24,
		OutputTokens:       12,
		InputTokensStdDev:  7,
		OutputTokensStdDev: 4,
		SharedPrefixTokens: 6,
	}
	first, err := newPerformanceWorkload(profile)
	if err != nil {
		t.Fatalf("newPerformanceWorkload() error = %v", err)
	}
	second, err := newPerformanceWorkload(profile)
	if err != nil {
		t.Fatalf("newPerformanceWorkload() second error = %v", err)
	}

	firstTargets := make([]performanceWorkloadTarget, 8)
	secondTargets := make([]performanceWorkloadTarget, 8)
	for index := range firstTargets {
		firstTargets[index] = first.target(uint64(index))
		secondTargets[index] = second.target(uint64(index))
		if firstTargets[index].InputTokens <= profile.SharedPrefixTokens || firstTargets[index].InputTokens > MaxPerformanceInputTokens {
			t.Fatalf("input target %d = %#v", index, firstTargets[index])
		}
		if firstTargets[index].OutputTokens == 0 || firstTargets[index].OutputTokens > MaxPerformanceOutputTokens {
			t.Fatalf("output target %d = %#v", index, firstTargets[index])
		}
	}
	if !reflect.DeepEqual(firstTargets, secondTargets) {
		t.Fatalf("same-seed targets differ: %#v != %#v", firstTargets, secondTargets)
	}

	differentProfile := profile
	differentProfile.RandomSeed++
	different, err := newPerformanceWorkload(differentProfile)
	if err != nil {
		t.Fatalf("newPerformanceWorkload(different seed) error = %v", err)
	}
	differentTargets := make([]performanceWorkloadTarget, len(firstTargets))
	for index := range differentTargets {
		differentTargets[index] = different.target(uint64(index))
	}
	if reflect.DeepEqual(firstTargets, differentTargets) {
		t.Fatalf("different-seed targets are identical: %#v", firstTargets)
	}
}

func TestNormalPerformancePromptUsesBoundedAllocations(t *testing.T) {
	workload, err := newPerformanceWorkload(PerformanceProfile{
		WorkloadMode: PerformanceWorkloadNormal, RandomSeed: 1,
		InputTokens: 10_000, OutputTokens: 1, SharedPrefixTokens: 5_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var prompt string
	allocations := testing.AllocsPerRun(3, func() {
		prompt = workload.prompt(7, 10_000)
	})
	if allocations > 12 {
		t.Fatalf("prompt allocations = %.1f, want bounded builder allocations", allocations)
	}
	if got := strings.Count(prompt, " ") + 1; got != 10_000 {
		t.Fatalf("prompt word count = %d", got)
	}
	if len(prompt) > 40_000 {
		t.Fatalf("compact prompt bytes = %d, want at most 4 bytes/word", len(prompt))
	}
}

func TestPerformanceTokenBudgetBlocksReleasesAndHonorsCancellation(t *testing.T) {
	budget := newPerformanceTokenBudget(10)
	cancelledContext, cancelImmediately := context.WithCancel(context.Background())
	cancelImmediately()
	if _, err := budget.acquire(cancelledContext, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled acquire error = %v", err)
	}
	release, err := budget.acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}

	blockedContext, cancel := context.WithCancel(context.Background())
	blocked := make(chan error, 1)
	go func() {
		_, acquireErr := budget.acquire(blockedContext, 1)
		blocked <- acquireErr
	}()
	select {
	case err := <-blocked:
		t.Fatalf("second acquire completed before release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-blocked; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire error = %v", err)
	}

	release()
	release() // release handles are idempotent.
	nextRelease, err := budget.acquire(context.Background(), 10)
	if err != nil {
		t.Fatalf("acquire after release error = %v", err)
	}
	nextRelease()
}

func TestNormalPerformanceWorkloadBuildsExactWordsWithSharedPrefixAndUniqueSuffix(t *testing.T) {
	profile := PerformanceProfile{
		WorkloadMode:       PerformanceWorkloadNormal,
		RandomSeed:         19,
		InputTokens:        12,
		OutputTokens:       5,
		SharedPrefixTokens: 4,
	}
	workload, err := newPerformanceWorkload(profile)
	if err != nil {
		t.Fatalf("newPerformanceWorkload() error = %v", err)
	}
	firstTarget := workload.target(2)
	secondTarget := workload.target(3)
	firstWords := strings.Fields(workload.prompt(2, firstTarget.InputTokens))
	secondWords := strings.Fields(workload.prompt(3, secondTarget.InputTokens))
	if len(firstWords) != int(firstTarget.InputTokens) || len(secondWords) != int(secondTarget.InputTokens) {
		t.Fatalf("word counts = %d/%d, targets = %d/%d", len(firstWords), len(secondWords), firstTarget.InputTokens, secondTarget.InputTokens)
	}
	if !reflect.DeepEqual(firstWords[:profile.SharedPrefixTokens], secondWords[:profile.SharedPrefixTokens]) {
		t.Fatalf("shared prefixes differ: %v != %v", firstWords, secondWords)
	}
	if reflect.DeepEqual(firstWords[profile.SharedPrefixTokens:], secondWords[profile.SharedPrefixTokens:]) {
		t.Fatalf("request suffixes are not unique: %v == %v", firstWords, secondWords)
	}
}
