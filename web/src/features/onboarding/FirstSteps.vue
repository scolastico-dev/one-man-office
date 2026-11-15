<script setup lang="ts">
// The first steps shown on a board without any ticket: add a project, check
// the agent setup, create a first ticket. Each step ticks itself off and has
// a button that does it. For now the steps are listed without ticks or
// buttons.
import FirstStepsPanel, { type FirstStep } from './FirstStepsPanel.vue'

const steps: FirstStep[] = [
  { id: 'project', title: 'Add a project', detail: 'A local git repository omo may work in.', done: false },
  { id: 'agents', title: 'Check the agent setup', detail: 'Which agent command-line tools and models the roles use.', done: false },
  { id: 'ticket', title: 'Create a first ticket', detail: 'A story for a goal, or a task for one piece of work.', done: false },
]
</script>

<template>
  <FirstStepsPanel :steps="steps" />
</template>
