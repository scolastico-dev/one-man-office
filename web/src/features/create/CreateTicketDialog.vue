<script setup lang="ts">
// The "Create ticket" dialog: type, title, project(s), story, description and
// the "More options" fields; on success it closes (unless "Create another"),
// shows a "#n created" toast with an "Open" action and emits `created`. The
// card itself arrives through the event stream. Not built yet: renders nothing.
defineProps<{
  open: boolean
  /** Story preset for a new task. */
  initialStory?: number
}>()
defineEmits<{ close: []; created: [n: number] }>()
</script>

<template></template>
