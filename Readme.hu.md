<p align="center"><img src="assets/logo.svg" alt="tsnmp logó" width="432"></p>

<p align="center">
  <a href="https://github.com/kzoli71/tsnmp/actions/workflows/build.yml"><img src="https://github.com/kzoli71/tsnmp/actions/workflows/build.yml/badge.svg?branch=main" alt="build"></a>
  <a href="https://github.com/kzoli71/tsnmp/actions/workflows/lint.yml"><img src="https://github.com/kzoli71/tsnmp/actions/workflows/lint.yml/badge.svg?branch=main" alt="lint"></a>
  <a href="https://github.com/kzoli71/tsnmp/releases/latest"><img src="https://img.shields.io/github/v/release/kzoli71/tsnmp" alt="legutóbbi kiadás"></a>
  <a href="https://github.com/kzoli71/tsnmp/commits/main"><img src="https://img.shields.io/github/last-commit/kzoli71/tsnmp" alt="utolsó commit"></a>
  <img src="https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white" alt="Go 1.24">
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20Linux-blue" alt="platform: Windows | Linux">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="licenc: MIT"></a>
</p>

<p align="center"><a href="Readme.md">English</a> | <b>Magyar</b></p>

# tsnmp – SNMP interfész-forgalomfigyelő

Szöveges felhasználói felületű (TUI) program, amely SNMPv2c vagy SNMPv3 protokollon
figyeli egy eszköz összes interfészét: In/Out bit/s és In/Out csomag/s.
Linuxon és Windows 10-en vagy újabbon fut, beleértve a Windows Server 2016-ot és az újabbakat
(Windows Terminal / konzol). Régebbi Windows-verziókat a Go 1.21+ nem támogat.

<p align="center"><img src="assets/screenshot-main.png" alt="tsnmp főképernyő: interfésztáblázat" width="984"></p>
<p align="center"><img src="assets/screenshot-graph.png" alt="tsnmp forgalmi grafikon" width="984"></p>

## Letöltés

Kész Linux és Windows (amd64) binárisok a
[Releases](https://github.com/kzoli71/tsnmp/releases) oldalon találhatók, a
`checksums.txt` (SHA-256) fájllal együtt. A `tsnmp -version` kiírja a program verzióját.

## Fordítás

Go 1.24 vagy újabb szükséges.

```sh
go build -trimpath -o tsnmp .                                  # Linux
GOOS=windows GOARCH=amd64 go build -trimpath -o tsnmp.exe .    # Windows (keresztfordítás)
go test ./...
```

A GitHub Actions (`.github/workflows/build.yml`) a `main` ágra történő minden pushnál
lefuttatja a teszteket, és elkészíti a Linux és Windows amd64 binárisokat; ezek a
futás „Artifacts” részéből tölthetők le.
Egy második workflow (`.github/workflows/lint.yml`) a `staticcheck` és a `gosec`
eszközt futtatja, a program fordításához használtnál újabb Go-val (1.26).
Egy `v*` tag (pl. `v1.0.1`) pusholása elindítja a `.github/workflows/release.yml`
workflow-t: az ellenőrzések után a [GoReleaser](https://goreleaser.com/)
(`.goreleaser.yaml`) elkészíti a kiadási archívumokat, az ellenőrzőösszegeket és a
változásnaplót, majd feltölti őket egy piszkozat (draft) GitHub release-be. Tagelés
előtt frissítsd a verziót a `versioninfo.json` fájlban, és futtasd a `go generate`
parancsot.

A Windows exe-t a release közzététele előtt Authenticode-dal aláírjuk egy Certum
SimplySign (felhős) kódaláíró tanúsítvánnyal, kézzel, a karbantartó gépén, a
`sign-release` szkripttel, amely ezen a repón kívül, külön van tárolva
(`kzoli71/sign-release`). Letölti a piszkozatból a Windows zipet, aláírja a
`tsnmp.exe`-t az [ssign](https://github.com/Le-Syl21/ssign) eszközzel (időbélyeggel a
`time.certum.pl`-től), az `osslsigncode`-dal ellenőrzi az aláírást, feltölti az
újracsomagolt zipet és a frissített `checksums.txt`-t, majd megerősítés után
közzéteszi a release-t. A SimplySign mobilalkalmazás aktuális 6 jegyű kódját
rejtett bevitellel kéri be:

```sh
CERTUM_EMAIL=te@example.com sign-release -R kzoli71/tsnmp \
    -a 'tsnmp_{version}_windows_amd64.zip' v1.0.1
```

A Windows exe-be verzióinformáció, manifest és a program ikonja kerül beágyazásra
(`versioninfo.json`, `tsnmp.exe.manifest`, `assets/tsnmp.ico` →
`resource_windows_*.syso`). Verzióinformáció és manifest nélkül a Defender nagyobb
eséllyel jelez téves riasztást (pl. `Trojan:Win32/Wacatac.B!ml`). Ezen fájlok
bármelyikének módosítása után újra kell generálni: `go generate`. Ne használd a
`-ldflags="-s -w"` kapcsolót: a strippelt exe gyanúsabbnak tűnik a víruskeresők számára.

### Tesztelés valódi eszköz nélkül

```sh
go run ./tools/fakeagent            # szimulált SNMP agent: 127.0.0.1:16161, community "public"
./tsnmp 127.0.0.1:16161             # vagy cím nélkül, a bejelentkező képernyőn
./tsnmp -v 3 -u tsnmp -A authpass123 -X privpass123 127.0.0.1:16161   # SNMPv3 (SHA/AES)
```

Kapcsolók: `-listen`, `-community`, `-extra` (további interfészek száma, alapértelmezés: 50),
`-delay` (várakozás minden válasz előtt, pl. `50ms`, lassú eszköz szimulálására),
`-refresh` (a számlálók csak ilyen időközönként változnak, pl. `2s`, mint egy router
számláló-gyorsítótára), `-hot` (egy 10 Gb/s-os port állandó 85%-os terheléssel), SNMPv3-hoz
pedig `-user` (alapértelmezés: `tsnmp`; `""` kikapcsolja az SNMPv3-at), `-auth` / `-authpass`
(alapértelmezés: `SHA` / `authpass123`), `-priv` / `-privpass` (alapértelmezés: `AES` /
`privpass123`; `-auth none` vagy `-priv none` csökkenti a biztonsági szintet), `-context`
(az egyetlen elfogadott context név, alapértelmezés: az alapértelmezett context). Az agent
egyszerre válaszol v2c-n a community-vel és v3-on a felhasználóval, és valódi eszközhöz
hasonlóan küldi az SNMPv3 hibajelentéseket (ismeretlen felhasználó, hibás jelszó, hibás
biztonsági szint, ismeretlen context).
Futás közben a standard bemeneten kiadható parancsok: `reset N` (az N. ifIndex számlálóinak
nullázása), `reboot` (agent újraindítása), `down` / `up` (kapcsolat megszakad /
helyreáll), `nodisc` / `disc` (a discovery kérések megválaszolásának leállítása / folytatása).

## Használat

```sh
tsnmp [-v 2c] [-c community] [cím[:port]]
tsnmp -v 3 -u felhasználó [-a proto -A jelszó [-x proto -X jelszó]] [-n context] [cím[:port]]
```

A cím IP-cím vagy hosztnév, opcionálisan porttal (alapértelmezett port: 161;
IPv6 esetén `[cím]:port`). Ha a cím meg van adva, a program azonnal csatlakozik,
különben a bejelentkező képernyőn kéri be a címet és az SNMP beállításokat
(bármelyik szövegmezőben az Enter csatlakozik).

Paraméterek nélkül indítva (`tsnmp`) a program sehova nem csatlakozik, és
használati útmutatót sem ír ki: a bejelentkező képernyőt nyitja meg az
alapértékekkel (SNMP verzió `v2c`, community `public`, üres cím), a kurzorral a
cím mezőben. A cím nélkül megadott kapcsolók (pl. `tsnmp -v 3 -u admin`) csak
kitöltik az űrlapot. Az Esc vagy a `Quit` gomb kilép; a `tsnmp -h` kiírja a
parancssori súgót.

Az `SNMP version` legördülő lista választja ki a mezőket:

- **v2c:** community (alapértelmezés: `public`).
- **v3:** felhasználónév, hitelesítési protokoll (`none`, `MD5`, `SHA`, `SHA224`,
  `SHA256`, `SHA384`, `SHA512`) és jelszó, titkosítási (privacy) protokoll
  (`none`, `DES`, `AES`, `AES192`, `AES256`, `AES192C`, `AES256C`) és jelszó,
  valamint a context név (üres: az alapértelmezett context; akkor kell, ha pl. az agent
  egy VRF-et / VPRN-t vagy más virtuális példányt egy nevesített context alatt szolgál ki).
  A biztonsági szint a protokollokból adódik: hitelesítés nélkül = noAuthNoPriv,
  hitelesítéssel, titkosítás nélkül = authNoPriv, mindkettővel = authPriv. A jelszavaknak
  legalább 8 karakteresnek kell lenniük. Az `AES192` / `AES256` a Blumenthal-féle
  kulcskiterjesztést, az `AES192C` / `AES256C` a Reeder-félét használja (ezt használja pl. a
  Cisco); az eszközök eltérnek, így ha az egyik visszafejtési hibát ad, próbáld a másikat.

A parancssorban ugyanezek a kapcsolók: `-v`, `-c`, `-u`, `-a` / `-A`, `-x` / `-X`, `-n`
(net-snmp stílusban; ha csak a jelszó van megadva, a protokoll alapértelmezése `SHA` / `AES`).
A `tsnmp -licenses` kiírja a licenceket (lásd [Licenc](#licenc)), majd kilép;
a `tsnmp -version` kiírja a verziót, majd kilép.
A parancssorban megadott jelszavakat a gép többi felhasználója is láthatja (folyamatlista):
közös használatú gépen inkább a bejelentkező képernyőn add meg őket. A program az SNMPv3
hibákat érthetően kiírja (pl. `authentication failed (wrong authentication password or protocol)`),
a fejléc pedig mutatja az SNMP verziót és a biztonsági beállításokat.

Főképernyő:

| Billentyű / egér          | Funkció                                   |
|---------------------------|-------------------------------------------|
| `+` / `-`, `[+]` / `[-]`  | Lekérdezési időköz beállítása (1–300 s, alapértelmezés: 5 s) |
| `↑` / `↓`                 | Mozgás az interfészek között              |
| `a` / `A`                 | Admin down interfészek mutatása/elrejtése (alapértelmezés: rejtve) |
| `o` / `O`                 | Oper down interfészek mutatása/elrejtése (alapértelmezés: látható) |
| `i` / `I`                 | Az Idx oszlop mutatása/elrejtése (alapértelmezés: rejtve) |
| `p` / `P`                 | A p/s oszlopok mutatása/elrejtése (keskeny képernyőhöz) |
| `/`                       | Keresés névben / leírásban (Enter: megtartja, Esc: törli) |
| `s` / `S`                 | Rendezés váltása: ifIndex, név, leírás, In/Out b/s, In%/Out%, In/Out p/s (forgalom és kihasználtság szerint csökkenő; rejtett p/s oszlopok esetén kimarad) |
| `r` / `R`                 | Mérés újraindítása megerősítéssel: minden mért adat és számláló törlődik (az interfészlista megmarad) |
| `F1`, `[F1 Help]`         | Súgó (a súgóban `l`: licencek)            |
| `Enter`, kattintás        | Az interfész forgalmi grafikonja          |
| `Esc`, `q`, `[Quit]`      | Kilépés megerősítéssel (aktív keresésnél az `Esc` előbb a keresést törli) |

Oszlopok: az `A` / `O` az admin / oper állapot (`U` = up, `D` = down), az `In%` / `Out%`
a kihasználtság az interfész sebességéhez képest (70% felett sárga, 90% felett piros;
`-`, ha az eszköz nem ad meg sebességet). Az alsó sor mutatja az utolsó lekérdezés idejét
és időtartamát; ha egy lekérdezés tovább tart, mint a lekérdezési időköz, sárga
figyelmeztetés jelenik meg. A leírás oszlop szélessége a képernyőhöz igazodik (10–30 karakter).

Ha megszakad a kapcsolat, piros `⚠ CONNECTION ERROR – last data: …` figyelmeztetés jelenik
meg a táblázat címében és a grafikon alján, a forgalmi értékek pedig szürkévé válnak.
Ha csak az interfészlista frissítése (discovery) sikertelen, a mérés az ismert listával
folytatódik, és a discovery 30 másodperc múlva újrapróbálkozik.

Ha egy interfész mért forgalma meghaladja az interfész sebességét, sárga figyelmeztetés
jelenik meg az alsó sorban és a grafikon alján (egy percig). Fizikai porton ez azt jelenti,
hogy a router a lekérdezési időköznél ritkábban frissíti a számlálóit (sok router csak
néhány másodpercenként frissíti az SNMP-n látható számlálókat), így az egyes mérések
felváltva túl magasak és túl alacsonyak (az átlaguk helyes). Megoldás: hosszabb
lekérdezési időköz (pl. 10 s vagy több).

Grafikon nézet:

| Billentyű / egér                   | Funkció                                       |
|------------------------------------|-----------------------------------------------|
| `←` / `→`                          | Görgetés vissza / előre egy ponttal           |
| `Shift+←` / `Shift+→`, `PgUp` / `PgDn` | Görgetés fél / teljes képernyőnyit        |
| `Home` / `End`                     | Élő nézet / legrégebbi mérés                  |
| `z` / `Z`                          | Nagyítás váltása előre / vissza: 1× → 5× → 30× → 120× → 1× (pontonként ennyi mérés átlaga) |
| `r` / `R`                          | Mérés újraindítása (megerősítéssel)           |
| `Esc`, jobb kattintás              | Vissza a listához                             |

Az x tengelyen néhány oszloponként időcímke áll: az abban az oszlopban ábrázolt pont
ideje (dátummal, ha nem a mai napról való). A pontok mérésenként egyenletesen
helyezkednek el, nem az idővel arányosan, így a lekérdezési időköz változása vagy egy
kimaradt lekérdezés után a címkék közötti idő eltérhet. A tengely vonalán látható a
nagyítás mértéke, a látható időtartam, valamint visszagörgetett állapotban (ilyenkor a
grafikon helyben marad, miközben a mérés folytatódik) a `◀ HISTORY` jelzés.

Bit/s és Packet/s panel (In = zöld, Out = kék, átfedés = sárga), folyamatosan frissül.
A jelmagyarázat az aktuális érték mellett az ábrázolt időszak minimumát és maximumát is
mutatja; az `Esc` vagy a jobb kattintás visszavisz a listához.

## Megjegyzések

- A 64 bites `ifHC*` számlálókat használja; ha az eszköz ezeket nem támogatja,
  interfészenként automatikusan a 32 bites `ifTable` számlálókra vált (átfordulás-kezeléssel).
- A forgalmi értékek az eszköz saját órájából (`sysUpTime`, minden kérésben lekérdezve)
  számolódnak, így a hálózati késleltetés és az újrapróbálkozások nem torzítják őket.
- Hamis tüskék ellen: az eszköz újraindulása (a `sysUpTime` visszaugrik), a számlálók
  nullázása (az `ifCounterDiscontinuityTime` megváltozik), vagy egy olyan 32 bites
  „átfordulás”, amely az interfész sebességénél nagyobb forgalmat jelentene, eldobja az
  adott mérést.
- Leírás: `ifAlias`, vagy ha az üres, `ifDescr`; 30 karakter felett levágva.
- Minden lekérdezés GetBulk kérésekkel, egyszerre több oszlopot kérve kérdezi le az összes
  interfész állapotát és számlálóit, így egy interfész összes számlálója ugyanabból a
  válaszból származik (az admin down interfészek is mérve vannak, csak alapértelmezésben
  rejtve). A csak 32 bites számlálókkal rendelkező interfészeket célzott GET kérésekkel
  kérdezi le, ha kevés van belőlük (legfeljebb 36), különben a teljes 32 bites tábla
  bejárásával.
- A nevek / leírások / sebességek 60 másodpercenként frissülnek (és amikor új interfész
  jelenik meg).
- Minden mért adat a program futása alatt a memóriában marad, tömör formában
  (mérésenként 20 bájt; 1000 interfész 5 s-os lekérdezéssel naponta kb. 0,35 GB).

## Licenc

MIT, lásd [LICENSE](LICENSE).

A tsnmp logóra és ikonra (az [assets/](assets/) könyvtár fájljai és a Windows exe-be
beágyazott ikon) nem vonatkozik az MIT licenc: © 2026 Kiss Zoltán, minden jog fenntartva.
Változtatás nélkül felhasználhatók a tsnmp projektre való hivatkozáshoz.

A tsnmp-be fordított harmadik féltől származó szoftverek (a Go standard könyvtár és a Go
modulok) licencei a [THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt) fájlban
találhatók. A program mindkét szöveget tartalmazza: a `tsnmp -licenses` kiírja őket, a
súgóban (`F1`) pedig az `l` megjeleníti. A fájl generált: a `go run ./tools/licenses -write`
újragenerálja, a `go run ./tools/licenses` ellenőrzi, hogy naprakész-e (a CI lefuttatja
az ellenőrzést).
