import * as React from "react"
import * as CheckboxPrimitive from "@radix-ui/react-checkbox"
import { cva, type VariantProps } from "class-variance-authority"
import { Check, Minus } from "lucide-react"

import { cn } from "@/lib/utils"

const checkboxVariants = cva(
  "peer inline-flex shrink-0 items-center justify-center border border-input ring-offset-background shadow transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground data-[state=indeterminate]:border-primary data-[state=indeterminate]:bg-primary data-[state=indeterminate]:text-primary-foreground",
  {
    variants: {
      size: {
        sm: "h-3.5 w-3.5",
        md: "h-4 w-4",
        lg: "h-5 w-5",
      },
      radius: {
        sm: "rounded-sm",
        md: "rounded",
        full: "rounded-full",
      },
    },
    defaultVariants: {
      size: "md",
      radius: "sm",
    },
  }
)

type CheckboxVariantProps = VariantProps<typeof checkboxVariants>
type CheckboxSize = NonNullable<CheckboxVariantProps["size"]>

const iconSizes: Record<CheckboxSize, string> = {
  sm: "h-3 w-3",
  md: "h-3.5 w-3.5",
  lg: "h-4 w-4",
}

export interface CheckboxProps
  extends React.ComponentPropsWithoutRef<typeof CheckboxPrimitive.Root>,
    CheckboxVariantProps {
  indicatorClassName?: string
  error?: boolean
}

const Checkbox = React.forwardRef<
  React.ElementRef<typeof CheckboxPrimitive.Root>,
  CheckboxProps
>(({ className, indicatorClassName, size = "md", radius, error, ...props }, ref) => (
  <CheckboxPrimitive.Root
    ref={ref}
    className={cn(
      checkboxVariants({ size, radius }),
      error &&
        "border-destructive focus-visible:ring-destructive data-[state=checked]:border-destructive data-[state=indeterminate]:border-destructive",
      className
    )}
    {...props}
  >
    <CheckboxPrimitive.Indicator
      className={cn(
        "group relative flex h-full w-full items-center justify-center text-current",
        indicatorClassName
      )}
    >
      <Check
        className={cn(
          "shrink-0 transition-transform duration-200 group-data-[state=indeterminate]:scale-0",
          iconSizes[size]
        )}
      />
      <Minus
        className={cn(
          "absolute shrink-0 scale-0 transition-transform duration-200 group-data-[state=indeterminate]:scale-100 group-data-[state=checked]:scale-0",
          iconSizes[size]
        )}
      />
    </CheckboxPrimitive.Indicator>
  </CheckboxPrimitive.Root>
))
Checkbox.displayName = CheckboxPrimitive.Root.displayName

export { Checkbox, checkboxVariants }
