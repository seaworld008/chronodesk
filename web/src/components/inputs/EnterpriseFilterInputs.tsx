import React from 'react'
import {
  Autocomplete,
  CircularProgress,
  MenuItem,
  TextField,
} from '@mui/material'
import {
  useChoicesContext,
  useInput,
  type RaRecord,
} from 'react-admin'

interface FilterInputBaseProps {
  source: string
  label: string
  alwaysOn?: boolean
  className?: string
  defaultValue?: unknown
  disabled?: boolean
  helperText?: React.ReactNode | false
  readOnly?: boolean
  record?: unknown
  resource?: string
  size?: 'small' | 'medium'
}

export interface FilterChoice {
  id: string | number
  name: string
}

export interface EnterpriseTextFilterInputProps extends FilterInputBaseProps {
  placeholder?: string
}

export const EnterpriseTextFilterInput = ({
  source,
  label,
  className,
  defaultValue,
  disabled,
  placeholder,
  readOnly,
  resource,
  size = 'small',
}: EnterpriseTextFilterInputProps) => {
  const { field, id } = useInput({
    source,
    resource,
    defaultValue,
    disabled,
    readOnly,
  })

  return (
    <TextField
      {...field}
      id={id}
      inputRef={field.ref}
      value={field.value ?? ''}
      className={['ra-input', `ra-input-${source}`, className].filter(Boolean).join(' ')}
      label={label}
      placeholder={placeholder}
      disabled={disabled}
      margin="dense"
      size={size}
      slotProps={{
        htmlInput: {
          'aria-label': label,
          readOnly,
        },
      }}
    />
  )
}

export interface EnterpriseSelectFilterInputProps extends FilterInputBaseProps {
  choices: FilterChoice[]
  emptyText?: string
}

export const EnterpriseSelectFilterInput = ({
  source,
  label,
  choices,
  className,
  defaultValue,
  disabled,
  emptyText = '全部',
  readOnly,
  resource,
  size = 'small',
}: EnterpriseSelectFilterInputProps) => {
  const { field, id } = useInput({
    source,
    resource,
    defaultValue,
    disabled,
    readOnly,
  })

  return (
    <TextField
      {...field}
      id={id}
      inputRef={field.ref}
      value={field.value ?? ''}
      className={['ra-input', `ra-input-${source}`, className].filter(Boolean).join(' ')}
      label={label}
      disabled={disabled}
      margin="dense"
      select
      size={size}
      slotProps={{
        htmlInput: {
          'aria-label': label,
          readOnly,
        },
      }}
    >
      <MenuItem value="">{emptyText}</MenuItem>
      {choices.map((choice) => (
        <MenuItem key={choice.id} value={choice.id}>
          {choice.name}
        </MenuItem>
      ))}
    </TextField>
  )
}

export interface EnterpriseBooleanFilterInputProps extends FilterInputBaseProps {
  trueText?: string
  falseText?: string
}

export const EnterpriseBooleanFilterInput = ({
  source,
  label,
  className,
  defaultValue,
  disabled,
  falseText = '否',
  readOnly,
  resource,
  size = 'small',
  trueText = '是',
}: EnterpriseBooleanFilterInputProps) => {
  const { field, id } = useInput({
    source,
    resource,
    defaultValue,
    disabled,
    readOnly,
  })
  const value =
    field.value === true ? 'true' : field.value === false ? 'false' : ''

  return (
    <TextField
      id={id}
      inputRef={field.ref}
      name={field.name}
      value={value}
      onBlur={field.onBlur}
      onChange={(event) => {
        const nextValue = event.target.value
        field.onChange(nextValue === '' ? null : nextValue === 'true')
      }}
      className={['ra-input', `ra-input-${source}`, className].filter(Boolean).join(' ')}
      label={label}
      disabled={disabled}
      margin="dense"
      select
      size={size}
      slotProps={{
        htmlInput: {
          'aria-label': label,
          readOnly,
        },
      }}
    >
      <MenuItem value="">全部</MenuItem>
      <MenuItem value="true">{trueText}</MenuItem>
      <MenuItem value="false">{falseText}</MenuItem>
    </TextField>
  )
}

export type EnterpriseDateFilterInputProps = FilterInputBaseProps

export const EnterpriseDateFilterInput = ({
  source,
  label,
  className,
  defaultValue,
  disabled,
  readOnly,
  resource,
  size = 'small',
}: EnterpriseDateFilterInputProps) => {
  const { field, id } = useInput({
    source,
    resource,
    defaultValue,
    disabled,
    readOnly,
  })

  return (
    <TextField
      {...field}
      id={id}
      inputRef={field.ref}
      value={field.value ?? ''}
      className={['ra-input', `ra-input-${source}`, className].filter(Boolean).join(' ')}
      label={label}
      disabled={disabled}
      margin="dense"
      size={size}
      type="date"
      slotProps={{
        htmlInput: {
          'aria-label': label,
          readOnly,
        },
        inputLabel: {
          shrink: true,
        },
      }}
    />
  )
}

export interface EnterpriseReferenceAutocompleteInputProps
  extends Omit<FilterInputBaseProps, 'source'> {
  source?: string
  optionText?: string | ((choice: RaRecord) => string)
  optionValue?: string
}

export const EnterpriseReferenceAutocompleteInput = ({
  source: sourceProp,
  label,
  className,
  defaultValue,
  disabled,
  optionText = 'name',
  optionValue = 'id',
  readOnly,
  resource: resourceProp,
  size = 'small',
}: EnterpriseReferenceAutocompleteInputProps) => {
  const {
    allChoices,
    isPending,
    resource,
    setFilters,
    source,
  } = useChoicesContext()
  const finalSource = sourceProp ?? source
  const finalResource = resourceProp ?? resource
  const { field, id } = useInput({
    source: finalSource,
    resource: finalResource,
    defaultValue,
    disabled,
    readOnly,
  })
  const choices = allChoices ?? []
  const selectedChoice = choices.find(
    (choice) => String(choice[optionValue]) === String(field.value),
  ) ?? null
  const getOptionLabel = (choice: RaRecord) => {
    if (typeof optionText === 'function') return optionText(choice)
    return String(choice[optionText] ?? choice.name ?? choice[optionValue] ?? '')
  }

  return (
    <Autocomplete
      id={id}
      className={['ra-input', `ra-input-${finalSource}`, className].filter(Boolean).join(' ')}
      options={choices}
      value={selectedChoice}
      disabled={disabled}
      loading={isPending}
      readOnly={readOnly}
      size={size}
      getOptionLabel={getOptionLabel}
      isOptionEqualToValue={(option, value) =>
        String(option[optionValue]) === String(value[optionValue])
      }
      noOptionsText="没有可选项"
      loadingText="正在加载"
      onBlur={field.onBlur}
      onChange={(_event, choice) => {
        field.onChange(choice?.[optionValue] ?? null)
      }}
      onInputChange={(_event, value, reason) => {
        if (reason === 'input' || reason === 'clear') {
          setFilters(value ? { q: value } : {}, undefined, true)
        }
      }}
      renderInput={(params) => (
        <TextField
          {...params}
          label={label}
          inputRef={field.ref}
          margin="dense"
          inputProps={{
            ...params.inputProps,
            'aria-label': label,
          }}
          InputProps={{
            ...params.InputProps,
            endAdornment: (
              <>
                {isPending ? <CircularProgress color="inherit" size={18} /> : null}
                {params.InputProps.endAdornment}
              </>
            ),
          }}
        />
      )}
    />
  )
}
