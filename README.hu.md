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

<p align="center"><a href="README.md">English</a> | <b>Magyar</b></p>

# tsnmp – SNMP interfész-forgalomfigyelő

Szöveges felhasználói felületű (TUI) program, amely SNMPv2c vagy SNMPv3 protokollon
figyeli egy eszköz összes interfészét: In/Out bit/s és In/Out csomag/s.
Linuxon és Windows 10-en vagy újabbon fut, beleértve a Windows Server 2016-ot és az újabbakat
(Windows Terminal / konzol). Régebbi Windows-verziókat a Go 1.21+ nem támogat.

<p align="center"><img src="assets/screenshot-main.png" alt="tsnmp főképernyő: interfésztáblázat" width="984"></p>
<p align="center"><img src="assets/screenshot-graph.png" alt="tsnmp forgalmi grafikon" width="984"></p>

## Főbb képességek

- Egy táblázatban az eszköz összes interfésze: In/Out bit/s, In/Out csomag/s és a
  kihasználtság az interfész sebességéhez képest, szűréssel, kereséssel és rendezéssel.
- SNMPv2c és SNMPv3 (MD5, SHA-1 és SHA-2 hitelesítés; DES és AES-128/192/256 titkosítás,
  a Blumenthal- és a Reeder-féle kulcskiterjesztéssel is; context nevek), érthető SNMPv3
  hibaüzenetekkel.
- 64 bites `ifHC*` számlálók, interfészenként automatikus visszaváltással a 32 bites
  `ifTable` számlálókra (átfordulás-kezeléssel).
- A forgalmi értékek az eszköz saját `sysUpTime` értékéből számolódnak, így a hálózati
  késleltetés és az újrapróbálkozások nem torzítják őket.
- Nincsenek hamis tüskék az eszköz újraindulása, a számlálók nullázása
  (`ifCounterDiscontinuityTime`) vagy egy valószínűtlen 32 bites átfordulás után.
- Interfészenkénti forgalmi grafikon a terminálban, a teljes mérési előzmény
  visszagörgetésével, nagyítással (átlagolással), minimum- és maximumértékekkel.
- Hatékony lekérdezés: GetBulk kérések, 1–300 s lekérdezési időköz.
- Egyetlen statikus bináris Linuxra és Windowsra, telepítés nélkül; a Windows exe
  kódaláírt.

## Letöltés

Kész Linux és Windows (amd64) binárisok a
[Releases](https://github.com/kzoli71/tsnmp/releases) oldalon találhatók, a
`checksums.txt` (SHA-256) fájllal együtt. A Windows exe Authenticode-dal aláírt (Certum
kódaláíró tanúsítvány). A `tsnmp -version` kiírja a program verzióját.

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

## Működés

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

## Fordítás forrásból

Go 1.24 vagy újabb szükséges.

```sh
go build -trimpath -o tsnmp .                                  # Linux
GOOS=windows GOARCH=amd64 go build -trimpath -o tsnmp.exe .    # Windows (keresztfordítás)
go test ./...
```

A valódi eszköz nélküli tesztelés (szimulált SNMP agent), a CI, a Windows-erőforrások és a
kiadási folyamat leírása a [DEVELOPMENT.md](DEVELOPMENT.md) fájlban található (angolul).

## Licenc

MIT, lásd [LICENSE](LICENSE).

A tsnmp logóra és ikonra (az [assets/](assets/) könyvtár fájljai és a Windows exe-be
beágyazott ikon) nem vonatkozik az MIT licenc: © 2026 Kiss Zoltán, minden jog fenntartva.
Változtatás nélkül felhasználhatók a tsnmp projektre való hivatkozáshoz.

A tsnmp-be fordított harmadik féltől származó szoftverek (a Go standard könyvtár és a Go
modulok) licencei a [THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt) fájlban
találhatók. A program mindkét szöveget tartalmazza: a `tsnmp -licenses` kiírja őket, a
súgóban (`F1`) pedig az `l` megjeleníti.
