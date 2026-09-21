import { AlertTriangle, Save } from "lucide-react";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { Button } from "./components/ui/button";

type DirtySection = {
  label: string;
  onSave: () => void;
  saving?: boolean;
};

type DirtySectionEntry = DirtySection & { key: string };

const SetDirtyContext = createContext<
  ((key: string, section: DirtySection | null) => void) | null
>(null);
const DirtySectionsContext = createContext<DirtySectionEntry[]>([]);

export function SettingsDirtyProvider({ children }: { children: ReactNode }) {
  const [byKey, setByKey] = useState<Record<string, DirtySection>>({});

  const setDirtySection = useCallback((key: string, section: DirtySection | null) => {
    setByKey((current) => {
      if (section === null) {
        if (!(key in current)) {
          return current;
        }
        const next = { ...current };
        delete next[key];
        return next;
      }
      const previous = current[key];
      if (
        previous &&
        previous.label === section.label &&
        previous.onSave === section.onSave &&
        previous.saving === section.saving
      ) {
        return current;
      }
      return { ...current, [key]: section };
    });
  }, []);

  const sections = useMemo(
    () => Object.entries(byKey).map(([key, section]) => ({ key, ...section })),
    [byKey],
  );

  return (
    <SetDirtyContext.Provider value={setDirtySection}>
      <DirtySectionsContext.Provider value={sections}>
        {children}
      </DirtySectionsContext.Provider>
    </SetDirtyContext.Provider>
  );
}

export function useDirtySection(
  key: string,
  dirty: boolean,
  label: string,
  onSave: () => void,
  saving = false,
) {
  const setDirtySection = useContext(SetDirtyContext);
  const onSaveRef = useRef(onSave);
  onSaveRef.current = onSave;

  useEffect(() => {
    if (!setDirtySection) {
      return;
    }
    if (!dirty) {
      setDirtySection(key, null);
      return;
    }
    setDirtySection(key, {
      label,
      onSave: () => onSaveRef.current(),
      saving,
    });
    return () => setDirtySection(key, null);
  }, [dirty, key, label, saving, setDirtySection]);
}

export function UnsavedSettingsBanner() {
  const sections = useContext(DirtySectionsContext);
  if (sections.length === 0) {
    return null;
  }

  return (
    <div
      role="alert"
      className="border-b-4 border-amber-800 bg-amber-400 px-6 py-3 text-amber-950 shadow-md lg:px-10"
    >
      <div className="flex flex-wrap items-center gap-3">
        <AlertTriangle className="h-7 w-7 shrink-0" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <p className="text-lg font-bold uppercase tracking-wide">Unsaved settings</p>
          <p className="text-sm font-semibold">
            Save your changes now. If you leave this page, these changes are lost.
            {" "}
            Open: {sections.map((section) => section.label).join(", ")}.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {sections.map((section) => (
            <Button
              key={section.key}
              size="sm"
              variant="primary"
              disabled={section.saving}
              onClick={section.onSave}
            >
              <Save className="h-4 w-4" aria-hidden="true" />
              Save {section.label}
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}
