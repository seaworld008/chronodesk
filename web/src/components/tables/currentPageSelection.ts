export type SelectionID = string | number

export const currentPageSelectionState = (
    selectableIds: readonly SelectionID[],
    selectedIds: readonly SelectionID[],
) => {
    const selectedOnCurrentPage = selectableIds.filter((id) =>
        selectedIds.includes(id),
    ).length
    const allSelected =
        selectableIds.length > 0 &&
        selectedOnCurrentPage === selectableIds.length
    return {
        selectedOnCurrentPage,
        allSelected,
        indeterminate: selectedOnCurrentPage > 0 && !allSelected,
    }
}

export const updateCurrentPageSelection = (
    selectedIds: readonly SelectionID[],
    currentPageIds: readonly SelectionID[],
    checked: boolean,
): SelectionID[] => {
    if (!checked) {
        return selectedIds.filter((id) => !currentPageIds.includes(id))
    }
    return [
        ...selectedIds,
        ...currentPageIds.filter((id) => !selectedIds.includes(id)),
    ]
}
