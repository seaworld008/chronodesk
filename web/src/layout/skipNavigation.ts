export const focusMainContent = () => {
  const content = document.getElementById('main-content')
  if (!content) return

  content.setAttribute('tabindex', '-1')
  content.addEventListener(
    'blur',
    () => content.removeAttribute('tabindex'),
    { once: true },
  )
  content.focus()
}
