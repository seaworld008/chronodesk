import React, {
  Children,
  cloneElement,
  isValidElement,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import {
  Datagrid,
  DatagridHeader,
  DatagridHeaderCell,
  useListContextWithProps,
} from 'react-admin'
import type { DatagridProps } from 'react-admin'
import {
  Checkbox,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
} from '@mui/material'
import type { TableProps } from '@mui/material'

export interface ResizableColumn {
  key: string
  defaultWidth: number
  minWidth?: number
  maxWidth?: number
  sticky?: 'left' | 'right'
}

interface NormalizedColumn extends ResizableColumn {
  minWidth: number
  maxWidth: number
}

const STORAGE_PREFIX = 'chronodesk.table-columns.v1'
const DEFAULT_MIN_WIDTH = 72
const DEFAULT_MAX_WIDTH = 560
const DEFAULT_COLUMN_WIDTH = 144

const normalizeColumns = (columns: ResizableColumn[]): NormalizedColumn[] =>
  columns.map((column) => ({
    ...column,
    minWidth: column.minWidth ?? DEFAULT_MIN_WIDTH,
    maxWidth: column.maxWidth ?? DEFAULT_MAX_WIDTH,
  }))

const clampWidth = (width: number, column: NormalizedColumn) =>
  Math.min(column.maxWidth, Math.max(column.minWidth, Math.round(width)))

const loadStoredWidths = (tableId: string, columns: NormalizedColumn[]) => {
  const defaults = Object.fromEntries(columns.map((column) => [column.key, column.defaultWidth]))
  if (typeof window === 'undefined') return defaults

  try {
    const stored = window.localStorage.getItem(`${STORAGE_PREFIX}.${tableId}`)
    if (!stored) return defaults

    const parsed = JSON.parse(stored) as Record<string, unknown>
    for (const column of columns) {
      const storedWidth = parsed[column.key]
      if (typeof storedWidth === 'number' && Number.isFinite(storedWidth)) {
        defaults[column.key] = clampWidth(storedWidth, column)
      }
    }
  } catch {
    // localStorage may be unavailable in hardened browser contexts. Defaults remain usable.
  }

  return defaults
}

const usePersistentColumnWidths = (tableId: string, rawColumns: ResizableColumn[]) => {
  const columns = useMemo(() => normalizeColumns(rawColumns), [rawColumns])
  const [widths, setWidths] = useState<Record<string, number>>(() => loadStoredWidths(tableId, columns))
  const [resizingKey, setResizingKey] = useState<string | null>(null)
  const resizeCleanupRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    setWidths((current) => {
      const next: Record<string, number> = {}
      for (const column of columns) {
        next[column.key] = clampWidth(current[column.key] ?? column.defaultWidth, column)
      }
      return next
    })
  }, [columns])

  useEffect(() => {
    const persistenceTimer = window.setTimeout(() => {
      try {
        window.localStorage.setItem(`${STORAGE_PREFIX}.${tableId}`, JSON.stringify(widths))
      } catch {
        // Persisting preferences is a progressive enhancement.
      }
    }, 150)

    return () => window.clearTimeout(persistenceTimer)
  }, [tableId, widths])

  useEffect(() => () => resizeCleanupRef.current?.(), [])

  const setColumnWidth = useCallback((key: string, width: number) => {
    const column = columns.find((item) => item.key === key)
    if (!column) return
    setWidths((current) => ({ ...current, [key]: clampWidth(width, column) }))
  }, [columns])

  const resetColumnWidth = useCallback((key: string) => {
    const column = columns.find((item) => item.key === key)
    if (!column) return
    setWidths((current) => ({ ...current, [key]: column.defaultWidth }))
  }, [columns])

  const startResize = useCallback((key: string, event: React.PointerEvent<HTMLElement>) => {
    const column = columns.find((item) => item.key === key)
    if (!column) return

    event.preventDefault()
    event.stopPropagation()
    resizeCleanupRef.current?.()

    const startX = event.clientX
    const startWidth = widths[key] ?? column.defaultWidth
    setResizingKey(key)
    document.body.classList.add('cd-table-is-resizing')

    const handlePointerMove = (pointerEvent: PointerEvent) => {
      setColumnWidth(key, startWidth + pointerEvent.clientX - startX)
    }
    const cleanup = () => {
      window.removeEventListener('pointermove', handlePointerMove)
      window.removeEventListener('pointerup', cleanup)
      window.removeEventListener('pointercancel', cleanup)
      document.body.classList.remove('cd-table-is-resizing')
      setResizingKey(null)
      resizeCleanupRef.current = null
    }

    resizeCleanupRef.current = cleanup
    window.addEventListener('pointermove', handlePointerMove)
    window.addEventListener('pointerup', cleanup)
    window.addEventListener('pointercancel', cleanup)
  }, [columns, setColumnWidth, widths])

  const handleResizeKeyDown = useCallback((key: string, event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    event.preventDefault()
    event.stopPropagation()
    const step = event.shiftKey ? 24 : 8
    const direction = event.key === 'ArrowRight' ? 1 : -1
    const column = columns.find((item) => item.key === key)
    if (!column) return
    setColumnWidth(key, (widths[key] ?? column.defaultWidth) + step * direction)
  }, [columns, setColumnWidth, widths])

  return {
    columns,
    widths,
    resizingKey,
    startResize,
    resetColumnWidth,
    handleResizeKeyDown,
  }
}

interface ColumnHeaderContentProps {
  column: NormalizedColumn
  label: React.ReactNode
  width: number
  resizing: boolean
  onResizeStart: (key: string, event: React.PointerEvent<HTMLElement>) => void
  onReset: (key: string) => void
  onKeyDown: (key: string, event: React.KeyboardEvent<HTMLElement>) => void
}

const ColumnHeaderContent = ({
  column,
  label,
  width,
  resizing,
  onResizeStart,
  onReset,
  onKeyDown,
}: ColumnHeaderContentProps) => (
  <span className="cd-resizable-header-content">
    <Tooltip title={typeof label === 'string' ? label : ''} enterDelay={600}>
      <span className="cd-resizable-header-label">{label}</span>
    </Tooltip>
    <span
      className="cd-column-resize-handle"
      data-resizing={resizing || undefined}
      role="separator"
      aria-label={`调整${typeof label === 'string' ? `“${label}”` : ''}列宽，当前 ${width} 像素`}
      aria-orientation="vertical"
      aria-valuemin={column.minWidth}
      aria-valuemax={column.maxWidth}
      aria-valuenow={width}
      tabIndex={0}
      onPointerDown={(event) => onResizeStart(column.key, event)}
      onDoubleClick={(event) => {
        event.preventDefault()
        event.stopPropagation()
        onReset(column.key)
      }}
      onClick={(event) => event.stopPropagation()}
      onKeyDown={(event) => onKeyDown(column.key, event)}
      title="拖动调整列宽，双击恢复默认；聚焦后可用方向键调整"
    />
  </span>
)

type DatagridHeaderProps = React.ComponentProps<typeof DatagridHeader>
type DatagridField = NonNullable<React.ComponentProps<typeof DatagridHeaderCell>['field']>
interface DatagridFieldProps {
  label?: React.ReactNode
  source?: string
  sortBy?: string
}

interface ResizableDatagridHeaderProps extends DatagridHeaderProps {
  columns: NormalizedColumn[]
  widths: Record<string, number>
  resizingKey: string | null
  onResizeStart: ColumnHeaderContentProps['onResizeStart']
  onReset: ColumnHeaderContentProps['onReset']
  onResizeKeyDown: ColumnHeaderContentProps['onKeyDown']
  syncTableWidth?: boolean
}

const ResizableDatagridHeader = ({
  children,
  className,
  hasExpand = false,
  hasBulkActions = false,
  isRowSelectable,
  columns,
  widths,
  resizingKey,
  onResizeStart,
  onReset,
  onResizeKeyDown,
  syncTableWidth = false,
  ...listContextProps
}: ResizableDatagridHeaderProps) => {
  const tableHeadRef = useRef<HTMLTableSectionElement>(null)
  const { sort, data, onSelect, selectedIds, setSort } = useListContextWithProps(listContextProps)

  const updateSort = useCallback((event: React.MouseEvent<HTMLElement>) => {
    event.stopPropagation()
    if (!setSort) return
    const field = event.currentTarget.dataset.field
    if (!field) return
    const order = sort?.field === field && sort.order === 'ASC'
      ? 'DESC'
      : (event.currentTarget.dataset.order === 'DESC' ? 'DESC' : 'ASC')
    setSort({ field, order })
  }, [setSort, sort])
  // React Admin memoizes DatagridHeaderCell using only sort-related props.
  // Refresh this callback when widths change so its memo comparator does not
  // incorrectly suppress the updated width, aria-valuenow, and header label.
  // Unlike changing the React key, this preserves focus during keyboard resize.
  const columnWidthRevision = Object.values(widths).join(',')
  const updateSortWithColumnWidths = useCallback(
    (event: React.MouseEvent<HTMLElement>) => {
      void columnWidthRevision
      updateSort(event)
    },
    [columnWidthRevision, updateSort],
  )

  const selectableIds = Array.isArray(data)
    ? data
      .filter((record) => !isRowSelectable || isRowSelectable(record))
      .map((record) => record.id)
    : []

  const handleSelectAll = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    if (!onSelect || !selectedIds || !data) return
    onSelect(
      event.target.checked
        ? selectedIds.concat(
          data
            .filter((record) =>
              !selectedIds.includes(record.id) &&
              (!isRowSelectable || isRowSelectable(record)))
            .map((record) => record.id),
        )
        : selectedIds.filter((id) => !data.some((record) => record.id === id)),
    )
  }, [data, isRowSelectable, onSelect, selectedIds])

  const fields = Children.toArray(children).filter(isValidElement) as DatagridField[]
  const totalWidth = columns.reduce(
    (sum, column) => sum + (widths[column.key] ?? column.defaultWidth),
    (hasBulkActions ? 52 : 0) + (hasExpand ? 52 : 0),
  )

  useLayoutEffect(() => {
    if (!syncTableWidth) return

    const table = tableHeadRef.current?.closest('table')
    if (!table) return

    // DatagridConfigurable owns the visible column set, so only its rendered
    // header knows the correct total. An explicit table width prevents
    // width:max-content from overriding the persisted per-column widths.
    table.style.width = `max(100%, ${totalWidth}px)`
    table.style.minWidth = `${totalWidth}px`
    table.style.tableLayout = 'fixed'
  }, [syncTableWidth, totalWidth])

  return (
    <TableHead ref={tableHeadRef} className={className}>
      <TableRow>
        {hasExpand && <TableCell padding="none" className="RaDatagrid-headerCell cd-table-fixed-column" />}
        {hasBulkActions && selectedIds && (
          <TableCell
            padding="checkbox"
            className="RaDatagrid-headerCell cd-table-selection-column"
          >
            <Checkbox
              slotProps={{ input: { 'aria-label': '选择当前页全部记录' } }}
              className="select-all"
              color="primary"
              checked={
                selectedIds.length > 0 &&
                selectableIds.length > 0 &&
                selectableIds.every((id) => selectedIds.includes(id))
              }
              onChange={handleSelectAll}
              onClick={(event) => event.stopPropagation()}
            />
          </TableCell>
        )}
        {fields.map((field, index) => {
          const column = columns[index]
          if (!column) return null
          const width = widths[column.key] ?? column.defaultWidth
          const typedField = field as unknown as React.ReactElement<DatagridFieldProps>
          const fieldProps = typedField.props
          const label = fieldProps.label ?? fieldProps.source ?? ''
          const resizableField = cloneElement(typedField, {
            label: (
              <ColumnHeaderContent
                column={column}
                label={label}
                width={width}
                resizing={resizingKey === column.key}
                onResizeStart={onResizeStart}
                onReset={onReset}
                onKeyDown={onResizeKeyDown}
              />
            ),
          }) as unknown as DatagridField

          return (
            <DatagridHeaderCell
              key={column.key}
              className={`RaDatagrid-headerCell cd-column-${column.key}`}
              field={resizableField}
              sort={sort}
              isSorting={sort?.field === (fieldProps.sortBy || fieldProps.source)}
              updateSort={setSort ? updateSortWithColumnWidths : undefined}
              sx={{
                boxSizing: 'border-box',
                width,
                minWidth: width,
                maxWidth: width,
                ...(column.sticky === 'right' && {
                  position: 'sticky',
                  right: 0,
                  zIndex: 4,
                }),
                ...(column.sticky === 'left' && {
                  position: 'sticky',
                  left: 0,
                  zIndex: 4,
                }),
              }}
            />
          )
        })}
      </TableRow>
    </TableHead>
  )
}

export interface PersistentResizableDatagridHeaderProps extends DatagridHeaderProps {
  tableId: string
  columnDefaults?: ResizableColumn[]
}

export const PersistentResizableDatagridHeader = ({
  tableId,
  columnDefaults = [],
  children,
  ...props
}: PersistentResizableDatagridHeaderProps) => {
  const columnsForVisibleFields = useMemo(() => {
    const defaults = new Map(columnDefaults.map((column) => [column.key, column]))
    return (Children.toArray(children).filter(isValidElement) as DatagridField[])
      .map((field, index) => {
        const fieldProps = (field as unknown as React.ReactElement<DatagridFieldProps>).props
        const key = fieldProps.source ||
          fieldProps.sortBy ||
          (typeof fieldProps.label === 'string' ? fieldProps.label : '') ||
          `column-${index + 1}`
        return defaults.get(key) ?? {
          key,
          defaultWidth: DEFAULT_COLUMN_WIDTH,
        }
      })
  }, [children, columnDefaults])
  const {
    columns,
    widths,
    resizingKey,
    startResize,
    resetColumnWidth,
    handleResizeKeyDown,
  } = usePersistentColumnWidths(tableId, columnsForVisibleFields)

  return (
    <ResizableDatagridHeader
      {...props}
      children={children}
      columns={columns}
      widths={widths}
      resizingKey={resizingKey}
      onResizeStart={startResize}
      onReset={resetColumnWidth}
      onResizeKeyDown={handleResizeKeyDown}
      syncTableWidth
    />
  )
}

export interface EnterpriseDatagridProps extends DatagridProps {
  tableId: string
  columns?: ResizableColumn[]
}

export const EnterpriseDatagrid = React.forwardRef<HTMLTableElement, EnterpriseDatagridProps>(
  ({ tableId, columns: configuredColumns, children, sx, ...props }, forwardedRef) => {
    const generatedColumns = useMemo(() => {
      const fields = Children.toArray(children).filter(isValidElement)
      return fields.map((field, index) => {
        const fieldProps = (field as React.ReactElement<{
          source?: string
          sortBy?: string
        }>).props
        return {
          key: fieldProps.source || fieldProps.sortBy || `column-${index + 1}`,
          defaultWidth: DEFAULT_COLUMN_WIDTH,
        }
      })
    }, [children])
    const rawColumns = configuredColumns?.length === generatedColumns.length
      ? configuredColumns
      : generatedColumns
    const {
      columns,
      widths,
      resizingKey,
      startResize,
      resetColumnWidth,
      handleResizeKeyDown,
    } = usePersistentColumnWidths(tableId, rawColumns)
    const totalWidth = columns.reduce(
      (sum, column) => sum + (widths[column.key] ?? column.defaultWidth),
      props.bulkActionButtons === false ? 0 : 52,
    )

    const Header = useMemo(() => {
      const HeaderComponent = (headerProps: DatagridHeaderProps) => (
        <ResizableDatagridHeader
          {...headerProps}
          columns={columns}
          widths={widths}
          resizingKey={resizingKey}
          onResizeStart={startResize}
          onReset={resetColumnWidth}
          onResizeKeyDown={handleResizeKeyDown}
        />
      )
      HeaderComponent.displayName = `ResizableDatagridHeader(${tableId})`
      return HeaderComponent
    }, [
      columns,
      handleResizeKeyDown,
      resetColumnWidth,
      resizingKey,
      startResize,
      tableId,
      widths,
    ])

    const rootSx = {
      width: '100%',
      maxWidth: '100%',
      minWidth: 0,
      '& .RaDatagrid-tableWrapper': {
        contain: 'inline-size',
        display: 'block',
        width: '100%',
        maxWidth: '100%',
        minWidth: 0,
        overflowX: 'auto',
      },
      '& .RaDatagrid-table': {
        tableLayout: 'fixed',
        width: `max(100%, ${totalWidth}px)`,
        minWidth: totalWidth,
      },
    }

    return (
      <Datagrid
        {...props}
        ref={forwardedRef}
        header={Header}
        className="cd-enterprise-table cd-enterprise-datagrid"
        sx={[rootSx, ...(Array.isArray(sx) ? sx : sx ? [sx] : [])]}
      >
        {children}
      </Datagrid>
    )
  },
)

EnterpriseDatagrid.displayName = 'EnterpriseDatagrid'

interface ResizableMuiTableProps extends Omit<TableProps, 'columns'> {
  tableId: string
  columns: ResizableColumn[]
}

const ResizableMuiTableInner = ({
  tableId,
  columns: rawColumns,
  children,
  sx,
  ...props
}: ResizableMuiTableProps) => {
  const {
    columns,
    widths,
    resizingKey,
    startResize,
    resetColumnWidth,
    handleResizeKeyDown,
  } = usePersistentColumnWidths(tableId, rawColumns)
  const totalWidth = columns.reduce(
    (sum, column) => sum + (widths[column.key] ?? column.defaultWidth),
    0,
  )
  let headerCellIndex = 0

  const enhancedChildren = Children.map(children, (child) => {
    if (!isValidElement(child)) return child

    if (child.type === TableHead) {
      const tableHead = child as React.ReactElement<React.ComponentProps<typeof TableHead>>
      return cloneElement(tableHead, {
        children: Children.map(tableHead.props.children, (row) => {
          if (!isValidElement(row) || row.type !== TableRow) return row
          const tableRow = row as React.ReactElement<React.ComponentProps<typeof TableRow>>

          return cloneElement(tableRow, {
            children: Children.map(tableRow.props.children, (cell) => {
              if (!isValidElement(cell) || cell.type !== TableCell) return cell
              const column = columns[headerCellIndex]
              headerCellIndex += 1
              if (!column) return cell
              const width = widths[column.key] ?? column.defaultWidth
              const tableCell = cell as React.ReactElement<React.ComponentProps<typeof TableCell>>

              return cloneElement(tableCell, {
                className: [
                  tableCell.props.className,
                  `cd-column-${column.key}`,
                  column.sticky ? `cd-table-sticky-${column.sticky}` : '',
                ].filter(Boolean).join(' '),
                sx: [
                  {
                    boxSizing: 'border-box',
                    width,
                    minWidth: width,
                    maxWidth: width,
                  },
                  ...(Array.isArray(tableCell.props.sx)
                    ? tableCell.props.sx
                    : tableCell.props.sx
                      ? [tableCell.props.sx]
                      : []),
                ],
                children: (
                  <ColumnHeaderContent
                    column={column}
                    label={tableCell.props.children}
                    width={width}
                    resizing={resizingKey === column.key}
                    onResizeStart={startResize}
                    onReset={resetColumnWidth}
                    onKeyDown={handleResizeKeyDown}
                  />
                ),
              })
            }),
          })
        }),
      })
    }

    if (child.type === TableBody) {
      const tableBody = child as React.ReactElement<React.ComponentProps<typeof TableBody>>
      return cloneElement(tableBody, {
        children: Children.map(tableBody.props.children, (row) => {
        if (!isValidElement(row) || row.type !== TableRow) return row
        const tableRow = row as React.ReactElement<React.ComponentProps<typeof TableRow>>
          let bodyCellIndex = 0

          return cloneElement(tableRow, {
            children: Children.map(tableRow.props.children, (cell) => {
              if (!isValidElement(cell) || cell.type !== TableCell) return cell
              const column = columns[bodyCellIndex]
              bodyCellIndex += 1
              if (!column) return cell
              const tableCell = cell as React.ReactElement<React.ComponentProps<typeof TableCell>>
              return cloneElement(tableCell, {
                className: [
                  tableCell.props.className,
                  `cd-column-${column.key}`,
                  column.sticky ? `cd-table-sticky-${column.sticky}` : '',
                ].filter(Boolean).join(' '),
              })
            })
          })
        }),
      })
    }

    return child
  })

  return (
    <Table
      {...props}
      className={`cd-enterprise-table ${props.className ?? ''}`.trim()}
      sx={[
        {
          tableLayout: 'fixed',
          width: `max(100%, ${totalWidth}px)`,
          minWidth: totalWidth,
        },
        ...(Array.isArray(sx) ? sx : sx ? [sx] : []),
      ]}
    >
      {enhancedChildren}
    </Table>
  )
}

export const ResizableMuiTable = (props: ResizableMuiTableProps) => (
  <ResizableMuiTableInner key={props.tableId} {...props} />
)

interface TruncatedTextProps {
  children: React.ReactNode
  title?: string
  maxWidth?: number | string
  fontWeight?: number
  color?: string
}

export const TruncatedText = ({
  children,
  title,
  maxWidth = '100%',
  fontWeight,
  color,
}: TruncatedTextProps) => {
  const tooltip = title ?? (typeof children === 'string' || typeof children === 'number' ? String(children) : '')

  return (
    <Tooltip title={tooltip} enterDelay={500} disableHoverListener={!tooltip}>
      <Typography
        component="span"
        variant="body2"
        noWrap
        sx={{
          display: 'block',
          maxWidth,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          fontWeight,
          color,
        }}
      >
        {children}
      </Typography>
    </Tooltip>
  )
}
