import { Languages } from 'lucide-react'
import { useI18n } from '@/lib/i18n'
import { Button } from '@/components/ui/button'

/** Shows the language you would switch to (zh ↔ en). */
export default function LangSwitch() {
  const { lang, setLang } = useI18n()
  return (
    <Button
      variant="ghost"
      size="sm"
      className="justify-start gap-3"
      onClick={() => setLang(lang === 'zh' ? 'en' : 'zh')}
    >
      <Languages className="h-4 w-4" />
      {lang === 'zh' ? 'English' : '中文'}
    </Button>
  )
}
