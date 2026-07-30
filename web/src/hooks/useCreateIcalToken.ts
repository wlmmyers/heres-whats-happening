import { useMutation } from '@tanstack/react-query';
import { createIcalToken } from '../api/ical';

export function useCreateIcalToken() {
  return useMutation({ mutationFn: createIcalToken });
}
