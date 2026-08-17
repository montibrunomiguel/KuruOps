import { useState } from "react";
import { useAuth } from "../auth/AuthContext";
import { api } from "../api/client";
import { useList, mutationErrorMessage } from "../api/hooks";
import { useConfirm } from "./useConfirm";

// Bundles the list/create-toggle/confirm-delete shape reimplemented from
// scratch across ~9 Settings panels: fetch the collection, toggle a create
// form, and delete a row at `${basePath}/${id}` with an inline confirm step
// (see useConfirm's own doc comment for why not window.confirm()). basePath
// is also where the list is fetched from, unless listPath is given
// separately -- some resources (e.g. tags) expose GET on a plain route but
// gate POST/DELETE behind a separate /settings/* admin route. Panels whose
// delete key isn't a plain id path segment, or that need more than
// list+create+delete, still write their own state -- this only replaces
// the boilerplate for the panels that fit it exactly.
export function useAdminCrud<T>(basePath: string, listPath: string = basePath) {
  const { token } = useAuth();
  const { data, loading, error, reload } = useList<T>(["admin-crud", listPath], (tk) => api.get<T[]>(listPath, tk));
  const [showCreate, setShowCreate] = useState(false);
  const { confirming, confirm, cancel } = useConfirm<string>();
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function remove(id: string) {
    setDeletingId(id);
    setDeleteError(null);
    try {
      await api.del(`${basePath}/${id}`, token);
      cancel();
      reload();
    } catch (err) {
      setDeleteError(mutationErrorMessage(err));
    } finally {
      setDeletingId(null);
    }
  }

  return {
    data,
    loading,
    error,
    reload,
    showCreate,
    setShowCreate,
    confirming,
    confirm,
    cancel,
    deletingId,
    deleteError,
    remove,
  };
}
