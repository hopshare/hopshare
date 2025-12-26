# TODO

## Now

* Pick the right UI now- simple, intuitive
* Keep testing Request lifecycle- edge cases, etc. 
* We should add some mocked email service- or an in-app messaging facility so that Members can communicate around a Request.
* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?

## Later

* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).

## Bugs

* An Organization Owner can request membership in their own Organization- this should be prevented

