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
install` and `npm ci` apply it through the `postinstall` script. Remove the
patch only after an upstream React Admin release no longer forwards these
properties, then rerun the browser console and axe health tests before
upgrading.
