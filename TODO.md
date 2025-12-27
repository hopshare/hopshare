# TODO

## Now

* Pick the right UI now- simple, intuitive
* Decide on terminology are they "Requests" or "Shares"? 
* Keep testing Request lifecycle- edge cases, etc. 
* Filter by Member in Requests page not working
* Requests should be clickable


## Later

* We should add some mocked email service- or an in-app messaging facility so that Members can communicate around a Request.
* Add ability to add comments to a completed Request.
* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).

## Bugs

* An Organization Owner can request membership in their own Organization- this should be prevented

