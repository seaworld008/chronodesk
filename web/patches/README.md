# Frontend dependency patches

`ra-ui-materialui+5.15.1.patch` keeps React Admin inputs compatible with
Material UI 9. Material UI 9 removed the legacy `InputProps` and
`InputLabelProps` TextField APIs, while React Admin 5.15.1 still forwards them
in addition to `slotProps`. React then reports the removed properties as
invalid DOM attributes.

The patch keeps the legacy properties for Material UI 5–7 and uses only
`slotProps` on Material UI 9. It also forwards Datagrid/DataTable row-selection
labels to the native checkbox input rather than the Material UI root span, so
screen readers receive the label without an invalid ARIA attribute. `npm
install` and `npm ci` apply it through the `postinstall` script.

For `AutocompleteInput`, the patch preserves all `params.slotProps` supplied by
Material UI 9 and deep-merges the `input`, `inputLabel`, and `htmlInput` slots.
The native input ref and ARIA handlers live in `htmlInput`; dropping that slot
causes the control to lose combobox behavior and logs
`Unable to find the input element`. Keep the full slot merge until React Admin
ships native Material UI 9 support.

Remove the patch only after an upstream React Admin release no longer forwards
the removed properties and preserves the Material UI 9 Autocomplete slots, then
rerun the browser console, combobox interaction, and axe health tests before
upgrading.
