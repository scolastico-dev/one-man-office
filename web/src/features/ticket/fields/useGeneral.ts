import { computed } from 'vue'
import { useBoard } from '../../../store'

/** The general settings the fields name defaults from, and whether the CEO is switched on. */
export function useGeneral() {
  const store = useBoard()
  const general = computed(() => store.state.general)
  const ceoEnabled = computed(() => general.value?.ceo.enabled === true)
  return { general, ceoEnabled }
}
